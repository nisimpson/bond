package toolbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	bond "github.com/nisimpson/bond"
)

// fileWriteInputSchema returns the JSON Schema for FileWriteInput with proper
// constraints: mode has enum ["write", "replace", "patch"], patches is an array
// of objects with start_line, end_line, and content.
// Requirements: FTE-5.2, FTE-5.4
func fileWriteInputSchema() json.Marshaler {
	min1 := float64(1)
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"path": {
				Type:        "string",
				Description: "file path to write",
			},
			"mode": {
				Type:        "string",
				Description: "write mode: write, replace, or patch",
				Enum:        []any{"write", "replace", "patch"},
			},
			"content": {
				Type:        "string",
				Description: "content to write (for write mode)",
			},
			"old_text": {
				Type:        "string",
				Description: "text to find (for replace mode)",
			},
			"new_text": {
				Type:        "string",
				Description: "replacement text (for replace mode)",
			},
			"patches": {
				Type:        "array",
				Description: "patch operations (for patch mode)",
				Items: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
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
						"content": {
							Type:        "string",
							Description: "replacement content for the line range",
						},
					},
					Required: []string{"start_line", "end_line", "content"},
				},
			},
		},
		Required: []string{"path"},
	}
}

// newFileWriteTool creates a file write tool with optional sandbox configuration.
// If cfg is nil, no base directory restriction is applied.
func newFileWriteTool(cfg *SandboxConfig) bond.Tool {
	t, _ := bond.NewFuncTool(
		func(ctx context.Context, input FileWriteInput) (FileWriteOutput, error) {
			// Requirements: TBOX-4.1, TBOX-4.2, TBOX-4.3, TBOX-4.4, TBOX-4.5, TBOX-4.6, TBOX-4.7
			// Requirements: FTE-4.1, FTE-4.2, FTE-4.3, FTE-4.4, FTE-4.5, FTE-4.6, FTE-4.7

			resolvedPath, err := resolveWritePath(input.Path, cfg)
			if err != nil {
				return FileWriteOutput{}, err
			}

			// FTE-4.1: Default empty mode to "write"
			if input.Mode == "" {
				input.Mode = "write"
			}

			// FTE-4.3: Validate mode is one of "write", "replace", "patch"
			switch input.Mode {
			case "write", "replace", "patch":
				// valid
			default:
				return FileWriteOutput{}, fmt.Errorf("unsupported mode; use write, replace, or patch: %w", ErrValidation)
			}

			// FTE-4.4, FTE-4.5, FTE-4.6, FTE-4.7: Validate required params per mode
			if err := validateWriteMode(input); err != nil {
				return FileWriteOutput{}, err
			}

			// Dispatch to mode handler
			switch input.Mode {
			case "replace":
				return runReplaceMode(resolvedPath, input)
			case "patch":
				return runPatchMode(resolvedPath, input)
			default:
				return runWriteMode(resolvedPath, input)
			}
		},
		bond.FuncToolOptions{
			Name:        ToolFileWrite,
			Description: "Write content to a file, creating it and any parent directories if they do not exist.",
			InputSchema: fileWriteInputSchema(),
		},
	)
	return t
}

// validateWriteMode checks that required parameters are present for the selected mode.
// Requirements: FTE-4.4, FTE-4.5, FTE-4.6, FTE-4.7
func validateWriteMode(input FileWriteInput) error {
	switch input.Mode {
	case "write":
		// Content is always present as a Go string; empty content is valid
		// (creates an empty file), preserving existing behavior.
	case "replace":
		if input.OldText == "" {
			return fmt.Errorf("replace mode requires old_text and new_text: %w", ErrValidation)
		}
	case "patch":
		if len(input.Patches) == 0 {
			return fmt.Errorf("patch mode requires patches: %w", ErrValidation)
		}
	}
	return nil
}

// runWriteMode performs the existing full-file write operation.
func runWriteMode(resolvedPath string, input FileWriteInput) (FileWriteOutput, error) {
	// Create parent directories if needed (TBOX-4.2)
	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0o755); err != nil {
		if os.IsPermission(err) {
			return FileWriteOutput{}, fmt.Errorf("permission denied: %s: %w", resolvedPath, ErrPermissionDenied)
		}
		return FileWriteOutput{}, err
	}

	// Write content to file — create or overwrite (TBOX-4.1, TBOX-4.3)
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
}

// runReplaceMode performs a find-and-replace operation on the file.
// Requirements: FTE-2.1, FTE-2.2, FTE-2.3, FTE-2.4, FTE-2.5, FTE-2.6, FTE-2.7, FTE-2.8, FTE-2.9
func runReplaceMode(resolvedPath string, input FileWriteInput) (FileWriteOutput, error) {
	// FTE-2.8: OldText must be non-empty
	if input.OldText == "" {
		return FileWriteOutput{}, fmt.Errorf("replace mode requires old_text and new_text: %w", ErrValidation)
	}

	// FTE-2.6: Read existing file; return ErrNotFound if missing
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return FileWriteOutput{}, fmt.Errorf("file not found: %s: %w", resolvedPath, ErrNotFound)
		}
		return FileWriteOutput{}, err
	}

	content := string(data)

	// Count occurrences of OldText (case-sensitive)
	count := strings.Count(content, input.OldText)

	// FTE-2.2: Return ErrNotFound if no match
	if count == 0 {
		return FileWriteOutput{}, fmt.Errorf("text to replace was not found: %w", ErrNotFound)
	}

	// FTE-2.3: Return ErrValidation if ambiguous (more than one match)
	if count > 1 {
		return FileWriteOutput{}, fmt.Errorf("old_text matches %d locations; must be unique: %w", count, ErrValidation)
	}

	// FTE-2.1, FTE-2.9: Replace the single occurrence (empty NewText deletes matched text)
	result := strings.Replace(content, input.OldText, input.NewText, 1)

	// Write result to file
	resultBytes := []byte(result)
	if err := os.WriteFile(resolvedPath, resultBytes, 0o644); err != nil {
		if os.IsPermission(err) {
			return FileWriteOutput{}, fmt.Errorf("permission denied: %s: %w", resolvedPath, ErrPermissionDenied)
		}
		return FileWriteOutput{}, err
	}

	// FTE-2.4: Return BytesWritten
	return FileWriteOutput{
		BytesWritten: len(resultBytes),
		Path:         resolvedPath,
	}, nil
}

// runPatchMode applies line-range patch operations to the file.
// Requirements: FTE-3.1, FTE-3.2, FTE-3.3, FTE-3.4, FTE-3.5, FTE-3.6, FTE-3.7, FTE-3.8, FTE-3.9, FTE-3.10, FTE-3.11, FTE-3.12
func runPatchMode(resolvedPath string, input FileWriteInput) (FileWriteOutput, error) {
	// FTE-3.6: Patches must be non-empty (safety check; also validated by validateWriteMode)
	if len(input.Patches) == 0 {
		return FileWriteOutput{}, fmt.Errorf("patch mode requires patches: %w", ErrValidation)
	}

	// FTE-3.9: Read existing file; return ErrNotFound if missing
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return FileWriteOutput{}, fmt.Errorf("file not found: %s: %w", resolvedPath, ErrNotFound)
		}
		return FileWriteOutput{}, err
	}

	// Split content into lines
	lines := strings.Split(string(data), "\n")
	totalLines := len(lines)

	// Make a copy of patches to avoid mutating the input
	patches := make([]PatchOperation, len(input.Patches))
	copy(patches, input.Patches)

	// Validate each patch
	for i := range patches {
		// FTE-3.4: StartLine must be >= 1
		if patches[i].StartLine < 1 {
			return FileWriteOutput{}, fmt.Errorf("start_line must be >= 1: %w", ErrValidation)
		}
		// FTE-3.11: EndLine must be >= StartLine
		if patches[i].EndLine < patches[i].StartLine {
			return FileWriteOutput{}, fmt.Errorf("end_line must be >= start_line: %w", ErrValidation)
		}
		// FTE-3.5: StartLine must be <= totalLines
		if patches[i].StartLine > totalLines {
			return FileWriteOutput{}, fmt.Errorf("start_line %d exceeds file length %d: %w", patches[i].StartLine, totalLines, ErrValidation)
		}
		// FTE-3.12: Clamp EndLine to totalLines if it exceeds
		if patches[i].EndLine > totalLines {
			patches[i].EndLine = totalLines
		}
	}

	// FTE-3.2: Sort patches by StartLine descending
	sort.Slice(patches, func(i, j int) bool {
		return patches[i].StartLine > patches[j].StartLine
	})

	// FTE-3.3: Check for overlapping ranges after sort
	// After sorting descending, for adjacent patches i and i+1,
	// patch[i+1] has a lower StartLine. Overlap occurs if patch[i+1].EndLine >= patch[i].StartLine.
	for i := 0; i < len(patches)-1; i++ {
		if patches[i+1].EndLine >= patches[i].StartLine {
			return FileWriteOutput{}, fmt.Errorf("patches overlap at lines %d-%d: %w",
				patches[i+1].StartLine, patches[i+1].EndLine, ErrValidation)
		}
	}

	// FTE-3.1, FTE-3.2: Apply patches bottom-up (already sorted descending)
	for _, p := range patches {
		// Replace lines[start-1:end] with split(patch.Content, "\n")
		newLines := strings.Split(p.Content, "\n")
		before := lines[:p.StartLine-1]
		after := lines[p.EndLine:]
		lines = make([]string, 0, len(before)+len(newLines)+len(after))
		lines = append(lines, before...)
		lines = append(lines, newLines...)
		lines = append(lines, after...)
	}

	// Join lines and write to file
	result := []byte(strings.Join(lines, "\n"))
	if err := os.WriteFile(resolvedPath, result, 0o644); err != nil {
		if os.IsPermission(err) {
			return FileWriteOutput{}, fmt.Errorf("permission denied: %s: %w", resolvedPath, ErrPermissionDenied)
		}
		return FileWriteOutput{}, err
	}

	// FTE-3.7: Return BytesWritten
	return FileWriteOutput{
		BytesWritten: len(result),
		Path:         resolvedPath,
	}, nil
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
