package acpio

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Requirement: 1.3 — THE ACP_Client SHALL provide a StdioProcess helper that spawns
// a subprocess and returns a Transport connected to its stdin/stdout.
// Requirement: 1.4 — WHEN using the Stdio_Process helper, THE Stdio_Process SHALL
// connect the subprocess's stderr to a configurable io.Writer for diagnostic output.
// Requirement: 1.5 — WHEN using the Stdio_Process helper, THE Stdio_Process SHALL
// allow the caller to configure environment variables for the subprocess.
// Requirement: 2.1 — WHEN the Stdio_Process is started with a command and arguments,
// THE Stdio_Process SHALL spawn the External_Agent as a subprocess with stdin and
// stdout connected as the Transport.
// Requirement: 2.2 — WHEN the Stdio_Process is closed, THE Stdio_Process SHALL send
// EOF to the subprocess's stdin and wait for the process to exit with a configurable timeout.
// Requirement: 2.3 — IF the subprocess does not exit within the configured timeout after
// stdin is closed, THEN THE Stdio_Process SHALL terminate the process forcefully using SIGKILL.
// Requirement: 2.4 — IF the subprocess exits unexpectedly during operation, THEN THE
// Stdio_Process SHALL return an error on the next Transport read or write indicating the
// process has terminated.
// Requirement: 2.5 — THE Stdio_Process SHALL capture the subprocess's exit code and make
// it available after shutdown.
// Requirement: 2.6 — THE Stdio_Process SHALL be safe to call Close multiple times without
// panicking (idempotent shutdown).

// defaultShutdownTimeout is the default time to wait for a subprocess to exit
// after closing its stdin before sending SIGKILL.
const defaultShutdownTimeout = 10 * time.Second

// StdioOptions configures a StdioProcess.
type StdioOptions struct {
	// Env contains additional environment variables in KEY=VALUE format.
	// These are appended to the current process's environment.
	Env []string

	// Stderr is the destination for the subprocess's stderr output.
	// Defaults to io.Discard if nil.
	Stderr io.Writer

	// Timeout is the graceful shutdown timeout before SIGKILL.
	// Defaults to 10 seconds if zero.
	Timeout time.Duration

	// Dir is the working directory for the subprocess.
	// If empty, inherits the current process's working directory.
	Dir string
}

// StdioProcess manages a subprocess and provides a ReadWriter connected to
// its stdin (write) and stdout (read). It implements ReadMessage/WriteMessage
// directly (satisfying agentacp.ReadWriter) and supports Reset for reconnection
// (satisfying agentacp.Resettable).
type StdioProcess struct {
	cmd     string
	args    []string
	env     []string
	stderr  io.Writer
	timeout time.Duration
	dir     string

	mu        sync.Mutex
	process   *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	transport *Transport
	exited    chan struct{}
	exitCode  int
	closed    bool
}

// NewStdioProcess creates a new StdioProcess with the given command, arguments,
// and options. The process is not started until Start is called.
func NewStdioProcess(command string, args []string, opts StdioOptions) *StdioProcess {
	stderr := opts.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultShutdownTimeout
	}

	return &StdioProcess{
		cmd:     command,
		args:    args,
		env:     opts.Env,
		stderr:  stderr,
		timeout: timeout,
		dir:     opts.Dir,
	}
}

// Start spawns the subprocess and creates the internal Transport.
// After Start returns successfully, the StdioProcess itself can be used as a
// ReadWriter (via ReadMessage/WriteMessage).
func (s *StdioProcess) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.startLocked()
}

// startLocked spawns the subprocess while holding the mutex.
func (s *StdioProcess) startLocked() error {
	cmd := exec.Command(s.cmd, s.args...)

	// Append caller-specified env vars to the current environment.
	cmd.Env = append(os.Environ(), s.env...)

	// Wire subprocess stderr to the configured writer.
	cmd.Stderr = s.stderr

	// Set working directory if configured.
	if s.dir != "" {
		cmd.Dir = s.dir
	}

	// Create stdin pipe: we write to stdinPipe, subprocess reads from its stdin.
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	// Create stdout pipe: subprocess writes to its stdout, we read from stdoutPipe.
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdinPipe.Close()
		return err
	}

	if err := cmd.Start(); err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		return err
	}

	s.process = cmd
	s.stdin = stdinPipe
	s.stdout = stdoutPipe
	s.exited = make(chan struct{})
	s.exitCode = 0
	s.closed = false

	// Start a goroutine that waits for process exit and captures the exit code.
	go s.waitForExit()

	// Transport reads from subprocess stdout, writes to subprocess stdin.
	s.transport = NewTransport(stdoutPipe, stdinPipe)
	return nil
}

// ReadMessage delegates to the internal Transport. Returns an error if the
// process has not been started.
func (s *StdioProcess) ReadMessage() (json.RawMessage, error) {
	s.mu.Lock()
	t := s.transport
	s.mu.Unlock()
	if t == nil {
		return nil, errors.New("acpio: process not started")
	}
	return t.ReadMessage()
}

// WriteMessage delegates to the internal Transport. Returns an error if the
// process has not been started.
func (s *StdioProcess) WriteMessage(msg json.RawMessage) error {
	s.mu.Lock()
	t := s.transport
	s.mu.Unlock()
	if t == nil {
		return errors.New("acpio: process not started")
	}
	return t.WriteMessage(msg)
}

// waitForExit blocks until the subprocess exits, captures the exit code,
// and closes the exited channel to signal completion.
func (s *StdioProcess) waitForExit() {
	err := s.process.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				s.exitCode = status.ExitStatus()
			} else {
				s.exitCode = 1
			}
		} else {
			s.exitCode = 1
		}
	} else {
		s.exitCode = 0
	}

	close(s.exited)
}

// Close performs an idempotent graceful shutdown of the subprocess.
// It closes stdin (sending EOF to the subprocess), waits for the process to
// exit within the configured timeout, and sends SIGKILL if it doesn't.
func (s *StdioProcess) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	stdin := s.stdin
	process := s.process
	exited := s.exited
	s.mu.Unlock()

	// Close stdin to signal EOF to the subprocess.
	if stdin != nil {
		_ = stdin.Close()
	}

	if process == nil || exited == nil {
		return nil
	}

	// Wait for the subprocess to exit within the timeout.
	select {
	case <-exited:
		// Process exited gracefully.
	case <-time.After(s.timeout):
		// Timeout expired; forcefully kill the process.
		if process.Process != nil {
			_ = process.Process.Kill()
		}
		// Wait for the kill to take effect.
		<-exited
	}

	return nil
}

// Reset closes the current subprocess and spawns a new one with the same
// configuration. This implements the Resettable interface for reconnection.
// After Reset, the StdioProcess itself remains the ReadWriter (it delegates to
// the newly created internal Transport).
func (s *StdioProcess) Reset() error {
	if err := s.Close(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Reset the closed flag so we can start a new process.
	s.closed = false

	return s.startLocked()
}

// ExitCode returns the exit code of the subprocess after it has exited.
// Returns 0 if the process has not yet exited or exited successfully.
func (s *StdioProcess) ExitCode() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitCode
}
