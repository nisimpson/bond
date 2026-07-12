package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	bond "github.com/nisimpson/bond"
)

// fileReadInputSchema returns the JSON Schema for FileReadInput with proper
// constraints: start_line and end_line are integer with minimum 1.
// Requirements: FTE-5.1, FTE-5.3
func fileReadInputSchema() json.Marshaler {
	min1 := float64(1)
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"path": {
				Type:        "string",
				Description: "file path to read",
			},
			"start_line": {
				Type:        "integer",
				Description: "start line (1-based inclusive)",
				Minimum:     &min1,
			},
			"end_line": {
				Type:        "integer",
				Description: "end line (1-based inclusive)",
				Minimum:     &min1,
			},
		},
		Required: []string{"path"},
	}
}

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

			content := string(data)

			// Requirements: FTE-1.1 through FTE-1.10 — line-range slicing
			if input.StartLine != nil || input.EndLine != nil {
				lines := strings.Split(content, "\n")
				totalLines := len(lines)

				// FTE-1.4: validate StartLine >= 1
				if input.StartLine != nil && *input.StartLine < 1 {
					return FileReadOutput{}, fmt.Errorf("start_line must be >= 1: %w", ErrValidation)
				}

				// FTE-1.5: validate EndLine >= StartLine when both set
				if input.StartLine != nil && input.EndLine != nil && *input.EndLine < *input.StartLine {
					return FileReadOutput{}, fmt.Errorf("end_line must be >= start_line: %w", ErrValidation)
				}

				// FTE-1.10: determine effective start (default 1)
				start := 1
				if input.StartLine != nil {
					start = *input.StartLine
				}

				// Determine effective end (default len(lines))
				end := totalLines
				if input.EndLine != nil {
					end = *input.EndLine
				}

				// FTE-1.6: if start > totalLines, return empty content
				if start > totalLines {
					return FileReadOutput{
						Content:    "",
						Path:       resolvedPath,
						TotalLines: &totalLines,
					}, nil
				}

				// FTE-1.7: clamp end to totalLines
				if end > totalLines {
					end = totalLines
				}

				// Extract the line slice and rejoin
				content = strings.Join(lines[start-1:end], "\n")

				// FTE-1.8: set TotalLines in output
				return FileReadOutput{
					Content:    content,
					Path:       resolvedPath,
					TotalLines: &totalLines,
				}, nil
			}

			// FTE-1.1: preserve full-file behavior when no line params provided
			return FileReadOutput{
				Content: content,
				Path:    resolvedPath,
			}, nil
		},
		bond.FuncToolOptions{
			Name:        ToolFileRead,
			Description: "Read the contents of a file and return them as a string.",
			InputSchema: fileReadInputSchema(),
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
