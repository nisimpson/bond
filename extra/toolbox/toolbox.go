// Package toolbox provides a set of reusable tools for Bond agents covering
// shell command execution, HTTP fetching, file I/O, and environment variable access.
package toolbox

import (
	"errors"
	"fmt"
	"time"

	bond "github.com/nisimpson/bond"
)

// Tool name constants for use with Options.Include filter.
const (
	ToolShell     = "shell"
	ToolHTTPFetch = "http_fetch"
	ToolFileRead  = "file_read"
	ToolFileWrite = "file_write"
	ToolEnv       = "env"
)

// Sentinel errors for type checking.
var (
	ErrPermissionDenied = errors.New("toolbox: permission denied")
	ErrTimeout          = errors.New("toolbox: timeout")
	ErrNotFound         = errors.New("toolbox: not found")
	ErrValidation       = errors.New("toolbox: validation error")
	ErrSizeExceeded     = errors.New("toolbox: size exceeded")
	ErrConnection       = errors.New("toolbox: connection error")
)

// SandboxConfig restricts tool behavior for security.
type SandboxConfig struct {
	// CommandAllowlist, if non-nil and non-empty, permits only these command binaries.
	CommandAllowlist []string
	// CommandDenylist is checked first and overrides the allowlist.
	CommandDenylist []string
	// ShellTimeout overrides the default 30s shell timeout.
	ShellTimeout time.Duration

	// BaseDirectory is the root directory for file operations.
	BaseDirectory string
	// MaxFileSize is the maximum file size in bytes for read/write operations.
	MaxFileSize int64

	// EnvAllowlist restricts which environment variables may be accessed.
	EnvAllowlist []string
}

// Options configures the toolbox plugin.
type Options struct {
	// Sandbox provides sandboxing configuration for shell and file tools.
	Sandbox *SandboxConfig
	// Include filters which tools are returned. Nil means all tools.
	Include []string
}

// ShellInput is the input schema for the shell tool.
type ShellInput struct {
	Command          string `json:"command" jsonschema:"required,the command to execute"`
	WorkingDirectory string `json:"working_directory,omitempty" jsonschema:"the working directory for command execution"`
	TimeoutSeconds   *int   `json:"timeout_seconds,omitempty" jsonschema:"timeout in seconds (1-3600)"`
}

// ShellOutput is the result of a shell command execution.
type ShellOutput struct {
	Output    string `json:"output"`
	ExitCode  int    `json:"exit_code"`
	Truncated bool   `json:"truncated,omitempty"`
}

// HTTPInput is the input schema for the HTTP tool.
type HTTPInput struct {
	URL            string            `json:"url" jsonschema:"required,the URL to fetch"`
	Method         string            `json:"method" jsonschema:"required,HTTP method (GET or POST)"`
	Headers        map[string]string `json:"headers,omitempty" jsonschema:"request headers"`
	Body           string            `json:"body,omitempty" jsonschema:"request body for POST"`
	TimeoutSeconds *int              `json:"timeout_seconds,omitempty" jsonschema:"timeout in seconds (1-300)"`
}

// HTTPOutput is the result of an HTTP request.
type HTTPOutput struct {
	StatusCode int    `json:"status_code"`
	Body       string `json:"body"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// FileReadInput is the input schema for the file read tool.
type FileReadInput struct {
	Path string `json:"path" jsonschema:"required,file path to read"`
}

// FileReadOutput is the result of a file read.
type FileReadOutput struct {
	Content string `json:"content"`
	Path    string `json:"path"`
}

// FileWriteInput is the input schema for the file write tool.
type FileWriteInput struct {
	Path    string `json:"path" jsonschema:"required,file path to write"`
	Content string `json:"content" jsonschema:"required,content to write"`
}

// FileWriteOutput is the result of a file write.
type FileWriteOutput struct {
	BytesWritten int    `json:"bytes_written"`
	Path         string `json:"path"`
}

// EnvInput is the input schema for the env tool.
type EnvInput struct {
	Name string `json:"name" jsonschema:"required,environment variable name"`
}

// EnvOutput is the result of an env variable lookup.
type EnvOutput struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// sandboxApplicableTools contains tool names that require a SandboxConfig.
var sandboxApplicableTools = map[string]bool{
	ToolShell:     true,
	ToolFileRead:  true,
	ToolFileWrite: true,
}

// Plugin is the toolbox plugin that bundles tools for Bond agents.
type Plugin struct {
	name  string
	tools []bond.Tool
}

// Name returns the plugin identifier.
func (p *Plugin) Name() string { return p.name }

// Tools returns the configured tool set.
func (p *Plugin) Tools() []bond.Tool { return p.tools }

// Init is a no-op; the toolbox plugin does not register any hooks.
func (p *Plugin) Init(registry *bond.HookRegistry) {}

// Ensure Plugin satisfies the bond.Plugin interface at compile time.
var _ bond.Plugin = (*Plugin)(nil)

// New creates a toolbox plugin configured with the given options.
// It returns an error if:
//   - sandbox-applicable tools are included but no SandboxConfig is provided (TBOX-6.7)
//   - the Include filter matches no available tools (TBOX-6.4)
func New(opts Options) (*Plugin, error) {
	// Requirements: TBOX-6.1, TBOX-6.2, TBOX-6.3, TBOX-6.4, TBOX-6.5, TBOX-6.6, TBOX-6.7

	// Determine which tools to include.
	included := resolveIncluded(opts.Include)

	// Validate that at least one tool is included.
	if len(included) == 0 {
		return nil, fmt.Errorf("toolbox: no tools matched the include filter")
	}

	// Check if the set contains sandbox-applicable tools.
	hasSandboxTools := false
	for name := range included {
		if sandboxApplicableTools[name] {
			hasSandboxTools = true
			break
		}
	}

	// TBOX-6.7: If sandbox-applicable tools in set but no Sandbox → error.
	if hasSandboxTools && opts.Sandbox == nil {
		return nil, fmt.Errorf("toolbox: sandbox configuration is required when shell or file tools are included")
	}

	// TBOX-6.6: If Sandbox provided but no sandbox-applicable tools → ignore sandbox (proceed).

	// Build the tool set.
	tools := buildTools(included, opts.Sandbox)

	return &Plugin{
		name:  "toolbox",
		tools: tools,
	}, nil
}

// resolveIncluded determines which tool names to include.
// If filter is nil, all 5 tools are included.
// Otherwise, only tool names that match one of the known constants are included.
func resolveIncluded(filter []string) map[string]bool {
	allTools := []string{ToolShell, ToolHTTPFetch, ToolFileRead, ToolFileWrite, ToolEnv}

	if filter == nil {
		result := make(map[string]bool, len(allTools))
		for _, name := range allTools {
			result[name] = true
		}
		return result
	}

	known := make(map[string]bool, len(allTools))
	for _, name := range allTools {
		known[name] = true
	}

	result := make(map[string]bool)
	for _, name := range filter {
		if known[name] {
			result[name] = true
		}
	}
	return result
}

// buildTools constructs the bond.Tool slice based on the included set and sandbox config.
func buildTools(included map[string]bool, sandbox *SandboxConfig) []bond.Tool {
	var tools []bond.Tool

	if included[ToolShell] {
		tools = append(tools, newShellTool(sandbox))
	}
	if included[ToolHTTPFetch] {
		tools = append(tools, newHTTPTool(0))
	}
	if included[ToolFileRead] {
		tools = append(tools, newFileReadTool(sandbox))
	}
	if included[ToolFileWrite] {
		tools = append(tools, newFileWriteTool(sandbox))
	}
	if included[ToolEnv] {
		if sandbox != nil {
			tools = append(tools, newEnvTool(sandbox.EnvAllowlist))
		} else {
			tools = append(tools, newEnvTool(nil))
		}
	}

	return tools
}
