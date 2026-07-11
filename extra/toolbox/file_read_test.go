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

func intPtr(i int) *int { return &i }

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

// TestFileRead_LineRange validates line-range reading behavior.
// Validates: FTE-1.1, FTE-1.2, FTE-1.3, FTE-1.4, FTE-1.5, FTE-1.6, FTE-1.7, FTE-1.8, FTE-1.10
func TestFileRead_LineRange(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "lines.txt")
	// 5 lines of content
	if err := os.WriteFile(filePath, []byte("line1\nline2\nline3\nline4\nline5"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tool := newFileReadTool(&SandboxConfig{BaseDirectory: dir})

	runRead := func(t *testing.T, input FileReadInput) (FileReadOutput, error) {
		t.Helper()
		raw, _ := json.Marshal(input)
		blocks, err := tool.Run(context.Background(), raw)
		if err != nil {
			return FileReadOutput{}, err
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
		return out, nil
	}

	t.Run("full file read with no params (backward compat)", func(t *testing.T) {
		// FTE-1.1: no start_line or end_line returns full file
		out, err := runRead(t, FileReadInput{Path: filePath})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := "line1\nline2\nline3\nline4\nline5"
		if out.Content != expected {
			t.Errorf("expected %q, got %q", expected, out.Content)
		}
		if out.TotalLines != nil {
			t.Errorf("expected TotalLines to be nil when no range specified, got %d", *out.TotalLines)
		}
	})

	t.Run("start_line only", func(t *testing.T) {
		// FTE-1.2: start_line without end_line returns from start to end of file
		out, err := runRead(t, FileReadInput{Path: filePath, StartLine: intPtr(3)})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := "line3\nline4\nline5"
		if out.Content != expected {
			t.Errorf("expected %q, got %q", expected, out.Content)
		}
		if out.TotalLines == nil || *out.TotalLines != 5 {
			t.Errorf("expected TotalLines=5, got %v", out.TotalLines)
		}
	})

	t.Run("end_line only", func(t *testing.T) {
		// FTE-1.10: end_line without start_line returns from line 1 through end_line
		out, err := runRead(t, FileReadInput{Path: filePath, EndLine: intPtr(3)})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := "line1\nline2\nline3"
		if out.Content != expected {
			t.Errorf("expected %q, got %q", expected, out.Content)
		}
		if out.TotalLines == nil || *out.TotalLines != 5 {
			t.Errorf("expected TotalLines=5, got %v", out.TotalLines)
		}
	})

	t.Run("both start and end", func(t *testing.T) {
		// FTE-1.3: both start_line and end_line returns the specified range
		out, err := runRead(t, FileReadInput{Path: filePath, StartLine: intPtr(2), EndLine: intPtr(4)})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := "line2\nline3\nline4"
		if out.Content != expected {
			t.Errorf("expected %q, got %q", expected, out.Content)
		}
		if out.TotalLines == nil || *out.TotalLines != 5 {
			t.Errorf("expected TotalLines=5, got %v", out.TotalLines)
		}
	})

	t.Run("start_line less than 1 returns ErrValidation", func(t *testing.T) {
		// FTE-1.4: start_line < 1 is a validation error
		raw, _ := json.Marshal(FileReadInput{Path: filePath, StartLine: intPtr(0)})
		_, err := tool.Run(context.Background(), raw)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrValidation) {
			t.Errorf("expected ErrValidation, got: %v", err)
		}
	})

	t.Run("end_line less than start_line returns ErrValidation", func(t *testing.T) {
		// FTE-1.5: end_line < start_line is a validation error
		raw, _ := json.Marshal(FileReadInput{Path: filePath, StartLine: intPtr(4), EndLine: intPtr(2)})
		_, err := tool.Run(context.Background(), raw)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrValidation) {
			t.Errorf("expected ErrValidation, got: %v", err)
		}
	})

	t.Run("start_line exceeds total lines returns empty content", func(t *testing.T) {
		// FTE-1.6: start > totalLines returns empty content with no error
		out, err := runRead(t, FileReadInput{Path: filePath, StartLine: intPtr(10)})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Content != "" {
			t.Errorf("expected empty content, got %q", out.Content)
		}
		if out.TotalLines == nil || *out.TotalLines != 5 {
			t.Errorf("expected TotalLines=5, got %v", out.TotalLines)
		}
	})

	t.Run("end_line exceeds total lines clamps to last line", func(t *testing.T) {
		// FTE-1.7: end_line > totalLines clamps to last line
		out, err := runRead(t, FileReadInput{Path: filePath, StartLine: intPtr(3), EndLine: intPtr(100)})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := "line3\nline4\nline5"
		if out.Content != expected {
			t.Errorf("expected %q, got %q", expected, out.Content)
		}
		if out.TotalLines == nil || *out.TotalLines != 5 {
			t.Errorf("expected TotalLines=5, got %v", out.TotalLines)
		}
	})

	t.Run("TotalLines set in output when range specified", func(t *testing.T) {
		// FTE-1.8: TotalLines is populated when start_line or end_line is set
		out, err := runRead(t, FileReadInput{Path: filePath, StartLine: intPtr(1), EndLine: intPtr(5)})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.TotalLines == nil {
			t.Fatal("expected TotalLines to be non-nil when range is specified")
		}
		if *out.TotalLines != 5 {
			t.Errorf("expected TotalLines=5, got %d", *out.TotalLines)
		}
	})
}
