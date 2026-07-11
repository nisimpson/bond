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

// Validates: TBOX-3.1
func TestFileRead_ValidUTF8File(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(filePath, []byte("hello world"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := newFileReadTool(&SandboxConfig{BaseDirectory: dir})
	input, _ := json.Marshal(FileReadInput{Path: filePath})
	blocks, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	tb, ok := blocks[0].(*bond.TextBlock)
	if !ok {
		t.Fatal("expected TextBlock")
	}
	var out FileReadOutput
	if err := json.Unmarshal([]byte(tb.Text), &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if out.Content != "hello world" {
		t.Errorf("expected content %q, got %q", "hello world", out.Content)
	}
	if out.Path == "" {
		t.Error("expected non-empty resolved path")
	}
}

// Validates: TBOX-3.2
func TestFileRead_FileNotFound(t *testing.T) {
	dir := t.TempDir()

	tool := newFileReadTool(&SandboxConfig{BaseDirectory: dir})
	input, _ := json.Marshal(FileReadInput{Path: filepath.Join(dir, "nonexistent.txt")})
	_, err := tool.Run(context.Background(), input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

// Validates: TBOX-3.3
func TestFileRead_PermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod not reliable on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("test not applicable when running as root")
	}

	dir := t.TempDir()
	filePath := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(filePath, []byte("secret"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Remove read permission from the file.
	if err := os.Chmod(filePath, 0000); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filePath, 0644) })

	tool := newFileReadTool(&SandboxConfig{BaseDirectory: dir})
	input, _ := json.Marshal(FileReadInput{Path: filePath})
	_, err := tool.Run(context.Background(), input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("expected ErrPermissionDenied, got: %v", err)
	}
}

// Validates: TBOX-3.4
func TestFileRead_SymlinkOutsideBaseDirectory(t *testing.T) {
	baseDir := t.TempDir()
	outsideDir := t.TempDir()

	// Create a file outside the base directory.
	outsideFile := filepath.Join(outsideDir, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside content"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Create a symlink inside baseDir pointing to the outside file.
	symlink := filepath.Join(baseDir, "link.txt")
	if err := os.Symlink(outsideFile, symlink); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := newFileReadTool(&SandboxConfig{BaseDirectory: baseDir})
	input, _ := json.Marshal(FileReadInput{Path: symlink})
	_, err := tool.Run(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for symlink pointing outside base directory")
	}
	if !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("expected ErrPermissionDenied, got: %v", err)
	}
}

// Validates: TBOX-3.4
func TestFileRead_PathTraversal(t *testing.T) {
	baseDir := t.TempDir()

	// Attempt path traversal with .. segments.
	tool := newFileReadTool(&SandboxConfig{BaseDirectory: baseDir})
	input, _ := json.Marshal(FileReadInput{Path: "../../../etc/passwd"})
	_, err := tool.Run(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for path traversal attempt")
	}
	// Path traversal should be rejected as either not found (if resolved path doesn't exist)
	// or permission denied (if it resolves outside base directory).
	if !errors.Is(err, ErrPermissionDenied) && !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrPermissionDenied or ErrNotFound, got: %v", err)
	}
}

// Validates: TBOX-3.5
func TestFileRead_FileSizeExceeded(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "big.txt")

	// Create a file larger than the configured max.
	maxSize := int64(100) // 100 bytes max
	data := make([]byte, maxSize+1)
	for i := range data {
		data[i] = 'A'
	}
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := newFileReadTool(&SandboxConfig{BaseDirectory: dir, MaxFileSize: maxSize})
	input, _ := json.Marshal(FileReadInput{Path: filePath})
	_, err := tool.Run(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for oversized file")
	}
	if !errors.Is(err, ErrSizeExceeded) {
		t.Errorf("expected ErrSizeExceeded, got: %v", err)
	}
}

// Validates: TBOX-3.7
func TestFileRead_NonUTF8Content(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "binary.bin")

	// Write invalid UTF-8 bytes.
	if err := os.WriteFile(filePath, []byte{0xff, 0xfe, 0x80, 0x81}, 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := newFileReadTool(&SandboxConfig{BaseDirectory: dir})
	input, _ := json.Marshal(FileReadInput{Path: filePath})
	_, err := tool.Run(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for non-UTF-8 content")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation, got: %v", err)
	}
}

// Validates: TBOX-3.6
func TestFileRead_RelativePathResolution(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	filePath := filepath.Join(subDir, "data.txt")
	if err := os.WriteFile(filePath, []byte("relative content"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Use a relative path that should resolve against the base directory.
	tool := newFileReadTool(&SandboxConfig{BaseDirectory: dir})
	input, _ := json.Marshal(FileReadInput{Path: "sub/data.txt"})
	blocks, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	tb, ok := blocks[0].(*bond.TextBlock)
	if !ok {
		t.Fatal("expected TextBlock")
	}
	var out FileReadOutput
	if err := json.Unmarshal([]byte(tb.Text), &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if out.Content != "relative content" {
		t.Errorf("expected content %q, got %q", "relative content", out.Content)
	}
}
