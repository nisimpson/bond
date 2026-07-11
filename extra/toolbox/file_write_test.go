package toolbox

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
