// Package builtin provides a suite of reusable tools for Bond agents that cover
// common system interactions: shell command execution, HTTP fetching, file I/O,
// and environment variable access.
//
// # Overview
//
// The toolbox is delivered as a [bond.Plugin] that bundles one or more tools based
// on configuration. Each tool implements [bond.Tool] and exposes a typed JSON Schema
// so LLM providers can generate correct tool calls.
//
// # Tools
//
// The following tools are available, referenced by their constant names:
//
//   - [ToolShell] ("shell") — Execute shell commands with configurable timeouts,
//     command allowlists/denylists, and output truncation.
//   - [ToolHTTPFetch] ("http_fetch") — Perform HTTP GET or POST requests with
//     configurable timeouts, custom headers, and response body truncation.
//   - [ToolFileRead] ("file_read") — Read file contents with optional line-range
//     selection (start_line/end_line) for targeted reading without loading entire
//     files into context.
//   - [ToolFileWrite] ("file_write") — Write files in three modes: full-write
//     (create/overwrite), replace (find-and-replace a unique text match), or patch
//     (apply multiple line-range edits in a single operation).
//   - [ToolEnv] ("env") — Read environment variables with optional allowlist
//     restrictions.
//
// # Usage
//
// Create a toolbox plugin with [New] and register it with a Bond agent:
//
//	plugin, err := builtin.New(builtin.Options{
//	    Sandbox: &builtin.SandboxConfig{
//	        BaseDirectory:    "/app/workspace",
//	        CommandAllowlist: []string{"go", "make", "git"},
//	    },
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	agent := bond.New(provider, bond.WithPlugins(plugin))
//
// # Tool Filtering
//
// Use [Options].Include to select a subset of tools:
//
//	plugin, _ := builtin.New(builtin.Options{
//	    Sandbox: &builtin.SandboxConfig{BaseDirectory: "/app"},
//	    Include: []string{builtin.ToolFileRead, builtin.ToolFileWrite},
//	})
//
// When Include is nil, all five tools are registered.
//
// # Sandbox Configuration
//
// A [SandboxConfig] is required whenever shell or file tools are included.
// It provides security boundaries:
//
//   - BaseDirectory restricts file read/write operations to a directory subtree.
//   - CommandAllowlist/CommandDenylist control which binaries the shell tool may execute.
//   - MaxFileSize caps the maximum file size for read operations (default 10 MB).
//   - EnvAllowlist restricts which environment variables may be accessed.
//   - ShellTimeout overrides the default 30-second command timeout.
//
// # File Write Modes
//
// The file write tool supports three modes selected via the "mode" input parameter:
//
//   - "write" (default) — Creates or overwrites the file with the provided content.
//     Parent directories are created automatically.
//   - "replace" — Finds a unique occurrence of old_text in the file and substitutes
//     it with new_text. Fails if the match is ambiguous (multiple occurrences) or
//     absent.
//   - "patch" — Applies one or more line-range edits (PatchOperations) to the file.
//     Patches are sorted and applied bottom-up to preserve line numbering. Overlapping
//     patches are rejected.
//
// # Error Handling
//
// All tools return sentinel errors that can be checked with [errors.Is]:
//
//   - [ErrPermissionDenied] — Operation blocked by sandbox or filesystem permissions.
//   - [ErrNotFound] — File or resource does not exist.
//   - [ErrValidation] — Invalid input parameters.
//   - [ErrTimeout] — Operation exceeded its time limit.
//   - [ErrSizeExceeded] — File exceeds the configured maximum size.
//   - [ErrConnection] — Network connectivity failure (HTTP tool).
package builtin
