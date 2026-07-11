package toolbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	bond "github.com/nisimpson/bond"
	"github.com/nisimpson/bond/tool"
)

const defaultMaxFileSize = 10 * 1024 * 1024 // 10 MB

// newFileReadTool creates a file read tool with optional sandbox configuration.
// If cfg is nil, no base directory restriction is applied and the default 10 MB
// max file size is used.
func newFileReadTool(cfg *SandboxConfig) bond.Tool {
	t, _ := bond.NewFuncTool(
		func(ctx context.Context, input FileReadInput) (FileReadOutput, error) {
			// Requirements: TBOX-3.1, TBOX-3.2, TBOX-3.3, TBOX-3.4, TBOX-3.5, TBOX-3.6, TBOX-3.7, TBOX-3.8

			resolvedPath, err := resolveReadPath(input.Path, cfg)
			if err != nil {
				return FileReadOutput{}, err
			}

			// Requirement: TBOX-3.2 — stat the file to check existence and size
			info, err := os.Stat(resolvedPath)
			if err != nil {
				if os.IsNotExist(err) {
					return FileReadOutput{}, fmt.Errorf("file not found: %s: %w", input.Path, ErrNotFound)
				}
				if os.IsPermission(err) {
					return FileReadOutput{}, fmt.Errorf("permission denied: %s: %w", resolvedPath, ErrPermissionDenied)
				}
				return FileReadOutput{}, err
			}

			// Requirement: TBOX-3.5, TBOX-3.8 — check file size against configured max or default
			maxSize := int64(defaultMaxFileSize)
			if cfg != nil && cfg.MaxFileSize > 0 {
				maxSize = cfg.MaxFileSize
			}
			if info.Size() > maxSize {
				return FileReadOutput{}, fmt.Errorf(
					"file size %d exceeds maximum %d bytes: %w",
					info.Size(), maxSize, ErrSizeExceeded,
				)
			}

			// Requirement: TBOX-3.1, TBOX-3.3 — read the file contents
			data, err := os.ReadFile(resolvedPath)
			if err != nil {
				if os.IsPermission(err) {
					return FileReadOutput{}, fmt.Errorf("permission denied: %s: %w", resolvedPath, ErrPermissionDenied)
				}
				return FileReadOutput{}, err
			}

			// Requirement: TBOX-3.7 — validate UTF-8 content
			if !utf8.Valid(data) {
				return FileReadOutput{}, fmt.Errorf("file contains invalid UTF-8: %s: %w", resolvedPath, ErrValidation)
			}

			return FileReadOutput{
				Content: string(data),
				Path:    resolvedPath,
			}, nil
		},
		bond.FuncToolOptions{
			Name:        ToolFileRead,
			Description: "Read the contents of a file and return them as a string.",
			InputSchema: tool.SchemaFor[FileReadInput](),
		},
	)
	return t
}

// resolveReadPath resolves the input path against the sandbox configuration.
// It handles relative path resolution, symlink resolution, and base directory
// containment checks.
func resolveReadPath(path string, cfg *SandboxConfig) (string, error) {
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

	// Resolve symlinks.
	resolved, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s: %w", path, ErrNotFound)
		}
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
