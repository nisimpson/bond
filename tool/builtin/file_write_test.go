package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	bond "github.com/nisimpson/bond"
)

// realTempDir returns a TempDir with symlinks resolved so that macOS
// /var -> /private/var doesn't trip up base directory containment checks.
func realTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlinks on temp dir: %v", err)
	}
	return real
}

func runFileWriteTool(t *testing.T, baseDir string, input FileWriteInput) (FileWriteOutput, error) {
	t.Helper()
	tool := newFileWriteTool(&SandboxConfig{BaseDirectory: baseDir})
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	blocks, err := tool.Run(context.Background(), raw)
	if err != nil {
		return FileWriteOutput{}, err
	}
	if len(blocks) == 0 {
		t.Fatal("expected at least one block in output")
	}
	tb, ok := blocks[0].(*bond.TextBlock)
	if !ok {
		t.Fatalf("expected *bond.TextBlock, got %T", blocks[0])
	}
	var out FileWriteOutput
	if err := json.Unmarshal([]byte(tb.Text), &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	return out, nil
}

// Validates: TBOX-4.1
func TestFileWriteTool_NewFile(t *testing.T) {
	dir := realTempDir(t)
	filePath := filepath.Join(dir, "new.txt")

	out, err := runFileWriteTool(t, dir, FileWriteInput{
		Path:    filePath,
		Content: "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.BytesWritten != 5 {
		t.Errorf("expected BytesWritten=5, got %d", out.BytesWritten)
	}
	if out.Path != filePath {
		t.Errorf("expected Path=%q, got %q", filePath, out.Path)
	}

	// Verify file content on disk.
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("expected file content %q, got %q", "hello", string(data))
	}
}

// Validates: TBOX-4.3
func TestFileWriteTool_OverwriteExistingFile(t *testing.T) {
	dir := realTempDir(t)
	filePath := filepath.Join(dir, "existing.txt")

	// Create an existing file.
	if err := os.WriteFile(filePath, []byte("old content"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	out, err := runFileWriteTool(t, dir, FileWriteInput{
		Path:    filePath,
		Content: "new content",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.BytesWritten != len("new content") {
		t.Errorf("expected BytesWritten=%d, got %d", len("new content"), out.BytesWritten)
	}

	// Verify overwritten content.
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "new content" {
		t.Errorf("expected %q, got %q", "new content", string(data))
	}
}

// Validates: TBOX-4.2
func TestFileWriteTool_ParentDirectoryCreation(t *testing.T) {
	dir := realTempDir(t)
	filePath := filepath.Join(dir, "a", "b", "c", "deep.txt")

	out, err := runFileWriteTool(t, dir, FileWriteInput{
		Path:    filePath,
		Content: "deep",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.BytesWritten != 4 {
		t.Errorf("expected BytesWritten=4, got %d", out.BytesWritten)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "deep" {
		t.Errorf("expected %q, got %q", "deep", string(data))
	}
}

// Validates: TBOX-4.4
func TestFileWriteTool_SymlinkContainment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test not supported on Windows")
	}

	baseDir := realTempDir(t)
	outsideDir := realTempDir(t)

	// Create a symlink inside baseDir pointing to outsideDir.
	symlinkPath := filepath.Join(baseDir, "escape")
	if err := os.Symlink(outsideDir, symlinkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	// Attempt to write through the symlink.
	targetPath := filepath.Join(symlinkPath, "evil.txt")
	_, err := runFileWriteTool(t, baseDir, FileWriteInput{
		Path:    targetPath,
		Content: "escaped",
	})
	if err == nil {
		t.Fatal("expected permission denied error, got nil")
	}
	if !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("expected ErrPermissionDenied, got: %v", err)
	}

	// Verify file was not created outside.
	if _, statErr := os.Stat(filepath.Join(outsideDir, "evil.txt")); statErr == nil {
		t.Error("file should not have been created outside base directory")
	}
}

// Validates: TBOX-4.4
func TestFileWriteTool_PathTraversalRejected(t *testing.T) {
	baseDir := realTempDir(t)

	// Use .. segments to escape.
	escapePath := filepath.Join(baseDir, "..", "outside.txt")
	_, err := runFileWriteTool(t, baseDir, FileWriteInput{
		Path:    escapePath,
		Content: "escaped",
	})
	if err == nil {
		t.Fatal("expected permission denied error, got nil")
	}
	if !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("expected ErrPermissionDenied, got: %v", err)
	}
}

// Validates: TBOX-4.5
func TestFileWriteTool_PermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission test not reliable on Windows")
	}

	dir := realTempDir(t)
	// Create a read-only directory.
	readOnlyDir := filepath.Join(dir, "readonly")
	if err := os.MkdirAll(readOnlyDir, 0o555); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Ensure cleanup can remove it.
	t.Cleanup(func() { _ = os.Chmod(readOnlyDir, 0o755) })

	filePath := filepath.Join(readOnlyDir, "forbidden.txt")
	_, err := runFileWriteTool(t, dir, FileWriteInput{
		Path:    filePath,
		Content: "nope",
	})
	if err == nil {
		t.Fatal("expected permission denied error, got nil")
	}
	if !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("expected ErrPermissionDenied, got: %v", err)
	}
}

// Validates: TBOX-4.7
func TestFileWriteTool_RelativePathResolution(t *testing.T) {
	dir := realTempDir(t)

	// Provide just a filename (not absolute) and verify it resolves against base directory.
	out, err := runFileWriteTool(t, dir, FileWriteInput{
		Path:    "relative.txt",
		Content: "resolved",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedPath := filepath.Join(dir, "relative.txt")
	if out.Path != expectedPath {
		t.Errorf("expected path %q, got %q", expectedPath, out.Path)
	}

	data, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "resolved" {
		t.Errorf("expected %q, got %q", "resolved", string(data))
	}
}

// Validates: FTE-4.1, FTE-4.3, FTE-4.4, FTE-4.5, FTE-4.6, FTE-4.7
func TestFileWriteTool_ModeDispatch(t *testing.T) {
	t.Run("default mode (empty string) behaves as write", func(t *testing.T) {
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "default_mode.txt")

		out, err := runFileWriteTool(t, dir, FileWriteInput{
			Path:    filePath,
			Mode:    "", // empty → default to "write"
			Content: "default mode content",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.BytesWritten != len("default mode content") {
			t.Errorf("expected BytesWritten=%d, got %d", len("default mode content"), out.BytesWritten)
		}

		data, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		if string(data) != "default mode content" {
			t.Errorf("expected %q, got %q", "default mode content", string(data))
		}
	})

	t.Run("unsupported mode returns ErrValidation", func(t *testing.T) {
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "unsupported.txt")

		for _, mode := range []string{"invalid", "delete", "append"} {
			_, err := runFileWriteTool(t, dir, FileWriteInput{
				Path:    filePath,
				Mode:    mode,
				Content: "test",
			})
			if err == nil {
				t.Fatalf("mode %q: expected error, got nil", mode)
			}
			if !errors.Is(err, ErrValidation) {
				t.Errorf("mode %q: expected ErrValidation, got: %v", mode, err)
			}
		}
	})

	t.Run("case sensitivity rejects Write and REPLACE and PATCH", func(t *testing.T) {
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "case.txt")

		for _, mode := range []string{"Write", "REPLACE", "PATCH", "Replace", "Patch"} {
			_, err := runFileWriteTool(t, dir, FileWriteInput{
				Path:    filePath,
				Mode:    mode,
				Content: "test",
			})
			if err == nil {
				t.Fatalf("mode %q: expected error, got nil", mode)
			}
			if !errors.Is(err, ErrValidation) {
				t.Errorf("mode %q: expected ErrValidation, got: %v", mode, err)
			}
		}
	})

	t.Run("write mode with empty content creates empty file", func(t *testing.T) {
		// FTE-4.4: write mode preserves backward compat; empty content = empty file
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "empty_content.txt")

		out, err := runFileWriteTool(t, dir, FileWriteInput{
			Path:    filePath,
			Mode:    "write",
			Content: "",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.BytesWritten != 0 {
			t.Errorf("expected BytesWritten=0, got %d", out.BytesWritten)
		}

		data, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		if len(data) != 0 {
			t.Errorf("expected empty file, got %q", string(data))
		}
	})

	t.Run("replace mode with missing old_text returns ErrValidation", func(t *testing.T) {
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "replace_missing.txt")
		// Create the file so the error is from validation, not "not found"
		if err := os.WriteFile(filePath, []byte("some content"), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		_, err := runFileWriteTool(t, dir, FileWriteInput{
			Path:    filePath,
			Mode:    "replace",
			OldText: "", // missing
			NewText: "replacement",
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrValidation) {
			t.Errorf("expected ErrValidation, got: %v", err)
		}
	})

	t.Run("patch mode with missing patches returns ErrValidation", func(t *testing.T) {
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "patch_missing.txt")
		if err := os.WriteFile(filePath, []byte("some content"), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		_, err := runFileWriteTool(t, dir, FileWriteInput{
			Path:    filePath,
			Mode:    "patch",
			Patches: nil, // missing
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrValidation) {
			t.Errorf("expected ErrValidation, got: %v", err)
		}
	})
}

// Validates: TBOX-4.6
func TestFileWriteTool_BytesWrittenCount(t *testing.T) {
	dir := realTempDir(t)

	tests := []struct {
		name    string
		content string
		want    int
	}{
		{"empty", "", 0},
		{"ascii", "hello world", 11},
		{"unicode", "héllo wörld", 13}, // multi-byte chars
		{"newlines", "line1\nline2\n", 12},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			filePath := filepath.Join(dir, tc.name+".txt")
			out, err := runFileWriteTool(t, dir, FileWriteInput{
				Path:    filePath,
				Content: tc.content,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.BytesWritten != tc.want {
				t.Errorf("expected BytesWritten=%d, got %d", tc.want, out.BytesWritten)
			}
		})
	}
}

// Validates: FTE-2.1, FTE-2.2, FTE-2.3, FTE-2.4, FTE-2.6, FTE-2.7, FTE-2.8, FTE-2.9
func TestFileWriteTool_ReplaceMode(t *testing.T) {
	t.Run("unique match replacement", func(t *testing.T) {
		// FTE-2.1: Replace single occurrence of old_text with new_text
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "replace.txt")
		if err := os.WriteFile(filePath, []byte("hello world"), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		out, err := runFileWriteTool(t, dir, FileWriteInput{
			Path:    filePath,
			Mode:    "replace",
			OldText: "hello",
			NewText: "goodbye",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// FTE-2.4: Returns bytes written and resolved path
		if out.Path != filePath {
			t.Errorf("expected Path=%q, got %q", filePath, out.Path)
		}
		expectedContent := "goodbye world"
		if out.BytesWritten != len(expectedContent) {
			t.Errorf("expected BytesWritten=%d, got %d", len(expectedContent), out.BytesWritten)
		}

		data, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		if string(data) != expectedContent {
			t.Errorf("expected %q, got %q", expectedContent, string(data))
		}
	})

	t.Run("no match returns ErrNotFound", func(t *testing.T) {
		// FTE-2.2: old_text not found in file
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "nomatch.txt")
		if err := os.WriteFile(filePath, []byte("hello world"), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		_, err := runFileWriteTool(t, dir, FileWriteInput{
			Path:    filePath,
			Mode:    "replace",
			OldText: "missing",
			NewText: "replacement",
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("expected ErrNotFound, got: %v", err)
		}
	})

	t.Run("ambiguous match returns ErrValidation", func(t *testing.T) {
		// FTE-2.3: old_text matches multiple locations
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "ambiguous.txt")
		if err := os.WriteFile(filePath, []byte("foo bar foo baz foo"), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		_, err := runFileWriteTool(t, dir, FileWriteInput{
			Path:    filePath,
			Mode:    "replace",
			OldText: "foo",
			NewText: "qux",
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrValidation) {
			t.Errorf("expected ErrValidation, got: %v", err)
		}
	})

	t.Run("empty OldText returns ErrValidation", func(t *testing.T) {
		// FTE-2.8: old_text must be non-empty
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "empty_old.txt")
		if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		_, err := runFileWriteTool(t, dir, FileWriteInput{
			Path:    filePath,
			Mode:    "replace",
			OldText: "",
			NewText: "new",
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrValidation) {
			t.Errorf("expected ErrValidation, got: %v", err)
		}
	})

	t.Run("empty NewText deletes matched text", func(t *testing.T) {
		// FTE-2.9: Empty new_text removes the matched text
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "delete.txt")
		if err := os.WriteFile(filePath, []byte("hello cruel world"), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		out, err := runFileWriteTool(t, dir, FileWriteInput{
			Path:    filePath,
			Mode:    "replace",
			OldText: " cruel",
			NewText: "",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expectedContent := "hello world"
		if out.BytesWritten != len(expectedContent) {
			t.Errorf("expected BytesWritten=%d, got %d", len(expectedContent), out.BytesWritten)
		}

		data, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		if string(data) != expectedContent {
			t.Errorf("expected %q, got %q", expectedContent, string(data))
		}
	})

	t.Run("file not found returns ErrNotFound", func(t *testing.T) {
		// FTE-2.6: Target file does not exist
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "nonexistent.txt")

		_, err := runFileWriteTool(t, dir, FileWriteInput{
			Path:    filePath,
			Mode:    "replace",
			OldText: "something",
			NewText: "other",
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("expected ErrNotFound, got: %v", err)
		}
	})

	t.Run("file unchanged on no match error", func(t *testing.T) {
		// FTE-2.7: File content preserved on error
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "unchanged_nomatch.txt")
		originalContent := "original content here"
		if err := os.WriteFile(filePath, []byte(originalContent), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		_, _ = runFileWriteTool(t, dir, FileWriteInput{
			Path:    filePath,
			Mode:    "replace",
			OldText: "missing text",
			NewText: "replacement",
		})

		data, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		if string(data) != originalContent {
			t.Errorf("file was modified; expected %q, got %q", originalContent, string(data))
		}
	})

	t.Run("file unchanged on ambiguous match error", func(t *testing.T) {
		// FTE-2.7: File content preserved on ambiguous match
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "unchanged_ambiguous.txt")
		originalContent := "abc abc abc"
		if err := os.WriteFile(filePath, []byte(originalContent), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		_, _ = runFileWriteTool(t, dir, FileWriteInput{
			Path:    filePath,
			Mode:    "replace",
			OldText: "abc",
			NewText: "xyz",
		})

		data, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		if string(data) != originalContent {
			t.Errorf("file was modified; expected %q, got %q", originalContent, string(data))
		}
	})

	t.Run("sandbox enforcement in replace mode", func(t *testing.T) {
		// FTE-2.5: Sandbox containment enforced for replace mode
		baseDir := realTempDir(t)
		outsideDir := realTempDir(t)

		// Create a file outside the sandbox
		outsideFile := filepath.Join(outsideDir, "secret.txt")
		if err := os.WriteFile(outsideFile, []byte("secret data"), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		// Attempt to replace in a file outside the sandbox using path traversal
		escapePath := filepath.Join(baseDir, "..", filepath.Base(outsideDir), "secret.txt")
		_, err := runFileWriteTool(t, baseDir, FileWriteInput{
			Path:    escapePath,
			Mode:    "replace",
			OldText: "secret",
			NewText: "public",
		})
		if err == nil {
			t.Fatal("expected permission denied error, got nil")
		}
		if !errors.Is(err, ErrPermissionDenied) {
			t.Errorf("expected ErrPermissionDenied, got: %v", err)
		}

		// Verify original file was not modified
		data, err := os.ReadFile(outsideFile)
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		if string(data) != "secret data" {
			t.Errorf("file outside sandbox was modified; expected %q, got %q", "secret data", string(data))
		}
	})
}

// Validates: FTE-3.1, FTE-3.2, FTE-3.3, FTE-3.4, FTE-3.5, FTE-3.6, FTE-3.7, FTE-3.9, FTE-3.10, FTE-3.11, FTE-3.12
func TestFileWriteTool_PatchMode(t *testing.T) {
	const testContent = "line1\nline2\nline3\nline4\nline5"

	t.Run("single patch application", func(t *testing.T) {
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "patch.txt")
		if err := os.WriteFile(filePath, []byte(testContent), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		out, err := runFileWriteTool(t, dir, FileWriteInput{
			Path: filePath,
			Mode: "patch",
			Patches: []PatchOperation{
				{StartLine: 2, EndLine: 3, Content: "replaced2\nreplaced3"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		data, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		expected := "line1\nreplaced2\nreplaced3\nline4\nline5"
		if string(data) != expected {
			t.Errorf("expected %q, got %q", expected, string(data))
		}
		if out.BytesWritten != len(expected) {
			t.Errorf("expected BytesWritten=%d, got %d", len(expected), out.BytesWritten)
		}
		if out.Path != filePath {
			t.Errorf("expected Path=%q, got %q", filePath, out.Path)
		}
	})

	t.Run("multiple non-overlapping patches", func(t *testing.T) {
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "patch.txt")
		if err := os.WriteFile(filePath, []byte(testContent), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		_, err := runFileWriteTool(t, dir, FileWriteInput{
			Path: filePath,
			Mode: "patch",
			Patches: []PatchOperation{
				{StartLine: 1, EndLine: 1, Content: "new1"},
				{StartLine: 4, EndLine: 5, Content: "new4\nnew5"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		data, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		expected := "new1\nline2\nline3\nnew4\nnew5"
		if string(data) != expected {
			t.Errorf("expected %q, got %q", expected, string(data))
		}
	})

	t.Run("overlapping patches return ErrValidation", func(t *testing.T) {
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "patch.txt")
		if err := os.WriteFile(filePath, []byte(testContent), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		_, err := runFileWriteTool(t, dir, FileWriteInput{
			Path: filePath,
			Mode: "patch",
			Patches: []PatchOperation{
				{StartLine: 1, EndLine: 3, Content: "a"},
				{StartLine: 3, EndLine: 5, Content: "b"},
			},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrValidation) {
			t.Errorf("expected ErrValidation, got: %v", err)
		}
	})

	t.Run("empty patches list returns ErrValidation", func(t *testing.T) {
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "patch.txt")
		if err := os.WriteFile(filePath, []byte(testContent), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		_, err := runFileWriteTool(t, dir, FileWriteInput{
			Path:    filePath,
			Mode:    "patch",
			Patches: []PatchOperation{},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrValidation) {
			t.Errorf("expected ErrValidation, got: %v", err)
		}
	})

	t.Run("StartLine less than 1 returns ErrValidation", func(t *testing.T) {
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "patch.txt")
		if err := os.WriteFile(filePath, []byte(testContent), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		_, err := runFileWriteTool(t, dir, FileWriteInput{
			Path: filePath,
			Mode: "patch",
			Patches: []PatchOperation{
				{StartLine: 0, EndLine: 2, Content: "x"},
			},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrValidation) {
			t.Errorf("expected ErrValidation, got: %v", err)
		}
	})

	t.Run("EndLine less than StartLine returns ErrValidation", func(t *testing.T) {
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "patch.txt")
		if err := os.WriteFile(filePath, []byte(testContent), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		_, err := runFileWriteTool(t, dir, FileWriteInput{
			Path: filePath,
			Mode: "patch",
			Patches: []PatchOperation{
				{StartLine: 3, EndLine: 2, Content: "x"},
			},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrValidation) {
			t.Errorf("expected ErrValidation, got: %v", err)
		}
	})

	t.Run("StartLine exceeding totalLines returns ErrValidation", func(t *testing.T) {
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "patch.txt")
		if err := os.WriteFile(filePath, []byte(testContent), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		_, err := runFileWriteTool(t, dir, FileWriteInput{
			Path: filePath,
			Mode: "patch",
			Patches: []PatchOperation{
				{StartLine: 10, EndLine: 12, Content: "x"},
			},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrValidation) {
			t.Errorf("expected ErrValidation, got: %v", err)
		}
	})

	t.Run("EndLine exceeding totalLines is clamped", func(t *testing.T) {
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "patch.txt")
		if err := os.WriteFile(filePath, []byte(testContent), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		_, err := runFileWriteTool(t, dir, FileWriteInput{
			Path: filePath,
			Mode: "patch",
			Patches: []PatchOperation{
				{StartLine: 4, EndLine: 100, Content: "clamped4\nclamped5"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		data, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		expected := "line1\nline2\nline3\nclamped4\nclamped5"
		if string(data) != expected {
			t.Errorf("expected %q, got %q", expected, string(data))
		}
	})

	t.Run("file not found returns ErrNotFound", func(t *testing.T) {
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "nonexistent.txt")

		_, err := runFileWriteTool(t, dir, FileWriteInput{
			Path: filePath,
			Mode: "patch",
			Patches: []PatchOperation{
				{StartLine: 1, EndLine: 1, Content: "x"},
			},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("expected ErrNotFound, got: %v", err)
		}
	})

	t.Run("file unchanged on error paths", func(t *testing.T) {
		dir := realTempDir(t)
		filePath := filepath.Join(dir, "patch.txt")
		if err := os.WriteFile(filePath, []byte(testContent), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		// Read original content before the failing operation
		before, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("read before: %v", err)
		}

		// Attempt overlapping patches (should fail)
		_, err = runFileWriteTool(t, dir, FileWriteInput{
			Path: filePath,
			Mode: "patch",
			Patches: []PatchOperation{
				{StartLine: 2, EndLine: 4, Content: "a"},
				{StartLine: 3, EndLine: 5, Content: "b"},
			},
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		// File should be unchanged
		after, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("read after: %v", err)
		}
		if string(before) != string(after) {
			t.Errorf("file was modified on error path: before=%q after=%q", string(before), string(after))
		}
	})

	t.Run("bottom-up ordering produces correct results regardless of input order", func(t *testing.T) {
		// Provide patches in ascending order (1, then 4) and verify result
		// is the same as providing them in descending order (4, then 1).
		dir := realTempDir(t)

		// Test with ascending order
		fileAsc := filepath.Join(dir, "asc.txt")
		if err := os.WriteFile(fileAsc, []byte(testContent), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}
		_, err := runFileWriteTool(t, dir, FileWriteInput{
			Path: fileAsc,
			Mode: "patch",
			Patches: []PatchOperation{
				{StartLine: 1, EndLine: 1, Content: "first"},
				{StartLine: 4, EndLine: 5, Content: "last"},
			},
		})
		if err != nil {
			t.Fatalf("ascending order error: %v", err)
		}
		ascData, err := os.ReadFile(fileAsc)
		if err != nil {
			t.Fatalf("read asc: %v", err)
		}

		// Test with descending order
		fileDesc := filepath.Join(dir, "desc.txt")
		if err := os.WriteFile(fileDesc, []byte(testContent), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}
		_, err = runFileWriteTool(t, dir, FileWriteInput{
			Path: fileDesc,
			Mode: "patch",
			Patches: []PatchOperation{
				{StartLine: 4, EndLine: 5, Content: "last"},
				{StartLine: 1, EndLine: 1, Content: "first"},
			},
		})
		if err != nil {
			t.Fatalf("descending order error: %v", err)
		}
		descData, err := os.ReadFile(fileDesc)
		if err != nil {
			t.Fatalf("read desc: %v", err)
		}

		// Both should produce the same result
		if string(ascData) != string(descData) {
			t.Errorf("ordering mismatch:\n  ascending: %q\n  descending: %q", string(ascData), string(descData))
		}

		// Verify the expected content
		expected := "first\nline2\nline3\nlast"
		if string(ascData) != expected {
			t.Errorf("expected %q, got %q", expected, string(ascData))
		}
	})
}
