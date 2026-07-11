package toolbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	bond "github.com/nisimpson/bond"
	"github.com/nisimpson/bond/tool"
)

// newFileWriteTool creates a file write tool with optional sandbox configuration.
// If cfg is nil, no base directory restriction is applied.
func newFileWriteTool(cfg *SandboxConfig) bond.Tool {
	t, _ := bond.NewFuncTool(
		func(ctx context.Context, input FileWriteInput) (FileWriteOutput, error) {
			// Requirements: TBOX-4.1, TBOX-4.2, TBOX-4.3, TBOX-4.4, TBOX-4.5, TBOX-4.6, TBOX-4.7

			resolvedPath, err := resolveWritePath(input.Path, cfg)
			if err != nil {
				return FileWriteOutput{}, err
			}

			// Requirement: TBOX-4.2 — create parent directories if needed
			if err := os.MkdirAll(filepath.Dir(resolvedPath), 0o755); err != nil {
				if os.IsPermission(err) {
					return FileWriteOutput{}, fmt.Errorf("permission denied: %s: %w", resolvedPath, ErrPermissionDenied)
				}
				return FileWriteOutput{}, err
			}

			// Requirement: TBOX-4.1, TBOX-4.3 — write content to file (create or overwrite)
			content := []byte(input.Content)
			if err := os.WriteFile(resolvedPath, content, 0o644); err != nil {
				if os.IsPermission(err) {
					return FileWriteOutput{}, fmt.Errorf("permission denied: %s: %w", resolvedPath, ErrPermissionDenied)
				}
				return FileWriteOutput{}, err
			}

			return FileWriteOutput{
				BytesWritten: len(content),
				Path:         resolvedPath,
			}, nil
		},
		bond.FuncToolOptions{
			Name:        ToolFileWrite,
			Description: "Write content to a file, creating it and any parent directories if they do not exist.",
			InputSchema: tool.SchemaFor[FileWriteInput](),
		},
	)
	return t
}

// resolveWritePath resolves the input path for write operations against the
// sandbox configuration. Unlike read paths, the target file may not exist yet,
// so symlink resolution is performed on the existing portion of the path.
func resolveWritePath(path string, cfg *SandboxConfig) (string, error) {
	var resolved string

	// Resolve relative paths against BaseDirectory or cwd.
	if !filepath.IsAbs(path) {
		if cfg != nil && cfg.BaseDirectory != "" {
			resolved = filepath.Join(cfg.BaseDirectory, path)
		} else {
			abs, err := filepath.Abs(path)
			if err != nil {
				return "", err
			}
			resolved = abs
		}
	} else {
		resolved = path
	}

	// Clean the path to normalize .. and . segments.
	resolved = filepath.Clean(resolved)

	// Resolve symlinks for the existing portion of the path.
	// Walk up from the full path until we find a component that exists,
	// resolve symlinks on that, then append the remaining components.
	resolved, err := resolveExistingSymlinks(resolved)
	if err != nil {
		return "", err
	}

	// If BaseDirectory is set, verify resolved path is within it.
	if cfg != nil && cfg.BaseDirectory != "" {
		resolvedBase, err := filepath.EvalSymlinks(cfg.BaseDirectory)
		if err != nil {
			return "", err
		}
		resolvedBase = filepath.Clean(resolvedBase)
		resolved = filepath.Clean(resolved)

		if resolved != resolvedBase && !strings.HasPrefix(resolved, resolvedBase+string(filepath.Separator)) {
			return "", fmt.Errorf("path %s is outside base directory: %w", path, ErrPermissionDenied)
		}
	}

	return resolved, nil
}

// resolveExistingSymlinks resolves symlinks for the longest existing prefix of
// the given path. For components that don't exist yet, they are appended as-is
// to the resolved existing prefix.
func resolveExistingSymlinks(path string) (string, error) {
	// Try resolving the full path first. If it works, the file/dir exists entirely.
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}

	// Walk up until we find an existing ancestor.
	remaining := []string{}
	current := path
	for {
		parent := filepath.Dir(current)
		remaining = append([]string{filepath.Base(current)}, remaining...)

		// If we've reached the root, resolve what we can.
		if parent == current {
			break
		}

		current = parent

		resolved, err = filepath.EvalSymlinks(current)
		if err == nil {
			// Found an existing ancestor; join remaining components.
			return filepath.Join(append([]string{resolved}, remaining...)...), nil
		}
	}

	// Nothing could be resolved; return the cleaned original path.
	return filepath.Clean(path), nil
}
