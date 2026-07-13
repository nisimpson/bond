package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/quick"

	bond "github.com/nisimpson/bond"
)

// pathGenerator generates paths that are likely to attempt sandbox escape.
// It produces paths with ".." segments, absolute paths, and mixed combinations.
type pathGenerator struct {
	Path string
}

// Generate implements quick.Generator for pathGenerator.
func (pathGenerator) Generate(rng *rand.Rand, size int) reflect.Value {
	segments := []string{
		"..", "../..", "../../..", "../../../..",
		".", "./", "../",
		"foo", "bar", "baz", "sub/dir",
		"/tmp", "/etc", "/var", "/usr",
		"/tmp/escape", "/etc/passwd",
		"..%2f", "..%00",
		"symlink", "link/../../../etc",
		"\x00", "null\x00byte",
		"a/b/c/../../../../..",
		"valid/../../..",
		"sub/../../../outside",
	}

	// Build a path from 1-5 random segments
	numSegments := rng.Intn(5) + 1
	parts := make([]string, numSegments)
	for i := range parts {
		parts[i] = segments[rng.Intn(len(segments))]
	}

	path := strings.Join(parts, "/")

	// Sometimes prefix with absolute path
	if rng.Float32() < 0.3 {
		path = "/" + path
	}

	return reflect.ValueOf(pathGenerator{Path: path})
}

// **Validates: Requirements TBOX-3.4, TBOX-4.4**

// TestProperty_SandboxContainment_FileRead verifies that resolveReadPath never
// resolves to a path outside the BaseDirectory. If the path escapes, the function
// must return an error; if it returns nil error, the path must be contained.
func TestProperty_SandboxContainment_FileRead(t *testing.T) {
	baseDir := t.TempDir()
	// Resolve the real path of baseDir (macOS /tmp is a symlink to /private/tmp)
	baseDir, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		t.Fatalf("failed to resolve baseDir symlinks: %v", err)
	}

	cfg := &SandboxConfig{BaseDirectory: baseDir}

	// Property: for any string path, if resolveReadPath succeeds, the result
	// must be within baseDir.
	f := func(path string) bool {
		resolved, err := resolveReadPath(path, cfg)
		if err != nil {
			// Rejection is correct behavior for unsafe paths.
			return true
		}
		// If resolution succeeded, the resolved path must be within baseDir.
		return resolved == baseDir || strings.HasPrefix(resolved, baseDir+string(filepath.Separator))
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Errorf("sandbox containment property violated for resolveReadPath: %v", err)
	}

	// Also test with the structured path generator that's more likely to escape.
	fg := func(pg pathGenerator) bool {
		resolved, err := resolveReadPath(pg.Path, cfg)
		if err != nil {
			return true
		}
		return resolved == baseDir || strings.HasPrefix(resolved, baseDir+string(filepath.Separator))
	}

	if err := quick.Check(fg, &quick.Config{MaxCount: 500}); err != nil {
		t.Errorf("sandbox containment property violated for resolveReadPath (structured): %v", err)
	}
}

// TestProperty_SandboxContainment_FileWrite verifies that resolveWritePath never
// resolves to a path outside the BaseDirectory. If the path escapes, the function
// must return an error; if it returns nil error, the path must be contained.
func TestProperty_SandboxContainment_FileWrite(t *testing.T) {
	baseDir := t.TempDir()
	// Resolve the real path of baseDir (macOS /tmp is a symlink to /private/tmp)
	baseDir, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		t.Fatalf("failed to resolve baseDir symlinks: %v", err)
	}

	cfg := &SandboxConfig{BaseDirectory: baseDir}

	// Property: for any string path, if resolveWritePath succeeds, the result
	// must be within baseDir.
	f := func(path string) bool {
		resolved, err := resolveWritePath(path, cfg)
		if err != nil {
			// Rejection is correct behavior for unsafe paths.
			return true
		}
		// If resolution succeeded, the resolved path must be within baseDir.
		return resolved == baseDir || strings.HasPrefix(resolved, baseDir+string(filepath.Separator))
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Errorf("sandbox containment property violated for resolveWritePath: %v", err)
	}

	// Also test with the structured path generator that's more likely to escape.
	fg := func(pg pathGenerator) bool {
		resolved, err := resolveWritePath(pg.Path, cfg)
		if err != nil {
			return true
		}
		return resolved == baseDir || strings.HasPrefix(resolved, baseDir+string(filepath.Separator))
	}

	if err := quick.Check(fg, &quick.Config{MaxCount: 500}); err != nil {
		t.Errorf("sandbox containment property violated for resolveWritePath (structured): %v", err)
	}
}

// lineRangeInput generates random file content and valid start/end line values
// for testing line-range containment.
type lineRangeInput struct {
	Lines     []string // file lines (at least 1)
	StartLine int      // 1-based start line
	EndLine   int      // 1-based end line, >= StartLine
}

// Generate implements quick.Generator for lineRangeInput.
func (lineRangeInput) Generate(rng *rand.Rand, size int) reflect.Value {
	// Generate 1 to 50 lines of random content
	numLines := rng.Intn(50) + 1
	lines := make([]string, numLines)
	for i := range lines {
		lineLen := rng.Intn(80)
		buf := make([]byte, lineLen)
		for j := range buf {
			buf[j] = byte(rng.Intn(94) + 32) // printable ASCII
		}
		lines[i] = string(buf)
	}

	// Generate a start_line: 1 to numLines+2 (allows testing beyond end of file)
	startLine := rng.Intn(numLines+2) + 1

	// Generate an end_line: startLine to numLines+5 (allows testing beyond end of file)
	endLine := startLine + rng.Intn(numLines+5-startLine+1)

	return reflect.ValueOf(lineRangeInput{
		Lines:     lines,
		StartLine: startLine,
		EndLine:   endLine,
	})
}

// **Validates: Requirements FTE-1.2, FTE-1.3, FTE-1.6, FTE-1.7**

// TestProperty_LineRangeContainment verifies that for any valid start_line and end_line,
// the returned content contains exactly `min(end_line, total_lines) - start_line + 1` lines
// when start_line <= total_lines, or empty content when start_line > total_lines.
func TestProperty_LineRangeContainment(t *testing.T) {
	baseDir := t.TempDir()
	baseDir, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		t.Fatalf("failed to resolve baseDir symlinks: %v", err)
	}

	cfg := &SandboxConfig{BaseDirectory: baseDir}
	readTool := newFileReadTool(cfg)

	var fileCounter int

	f := func(input lineRangeInput) bool {
		// Write the random content to a temp file
		content := strings.Join(input.Lines, "\n")
		fileCounter++
		filePath := filepath.Join(baseDir, "test_line_range.txt")
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			return false
		}
		defer os.Remove(filePath)

		totalLines := len(input.Lines)
		startLine := input.StartLine
		endLine := input.EndLine

		// Call the tool with start_line and end_line
		raw, _ := json.Marshal(FileReadInput{
			Path:      filePath,
			StartLine: &startLine,
			EndLine:   &endLine,
		})
		blocks, err := readTool.Run(context.Background(), raw)
		if err != nil {
			t.Logf("unexpected error: %v (start=%d, end=%d, total=%d)",
				err, startLine, endLine, totalLines)
			return false
		}

		if len(blocks) != 1 {
			t.Logf("expected 1 block, got %d", len(blocks))
			return false
		}
		tb, ok := blocks[0].(*bond.TextBlock)
		if !ok {
			t.Log("expected TextBlock")
			return false
		}

		var out FileReadOutput
		if err := json.Unmarshal([]byte(tb.Text), &out); err != nil {
			t.Logf("unmarshal output: %v", err)
			return false
		}

		// FTE-1.6: if start > totalLines, content should be empty
		if startLine > totalLines {
			if out.Content != "" {
				t.Logf("expected empty content for start=%d > total=%d, got %q",
					startLine, totalLines, out.Content)
				return false
			}
			return true
		}

		// FTE-1.7: clamp end to totalLines
		effectiveEnd := endLine
		if effectiveEnd > totalLines {
			effectiveEnd = totalLines
		}

		// Expected number of lines: min(end, totalLines) - start + 1
		expectedLineCount := effectiveEnd - startLine + 1

		// Count actual lines in the returned content.
		// Note: an empty string from joining a single empty line is still 1 line.
		// We only report 0 lines when expectedLineCount is 0 (which can't happen here
		// since start <= totalLines and end >= start).
		actualLineCount := strings.Count(out.Content, "\n") + 1

		if actualLineCount != expectedLineCount {
			t.Logf("line count mismatch: expected %d, got %d (start=%d, end=%d, total=%d)",
				expectedLineCount, actualLineCount, startLine, endLine, totalLines)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 300}); err != nil {
		t.Errorf("line-range containment property violated: %v", err)
	}
}

// **Validates: Requirements FTE-2.5, FTE-3.8**

// TestProperty_SandboxInvariance_WriteMode verifies that all three write modes
// ("write", "replace", "patch") enforce identical sandbox containment. For any
// given path, either ALL modes reject it with ErrPermissionDenied, or ALL modes
// accept the path (subsequent errors like ErrNotFound are acceptable — they mean
// the path was allowed but the file doesn't exist).
func TestProperty_SandboxInvariance_WriteMode(t *testing.T) {
	baseDir := t.TempDir()
	baseDir, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		t.Fatalf("failed to resolve baseDir symlinks: %v", err)
	}

	cfg := &SandboxConfig{BaseDirectory: baseDir}
	writeTool := newFileWriteTool(cfg)

	f := func(pg pathGenerator) bool {
		path := pg.Path

		// Attempt write mode
		writeRaw, _ := json.Marshal(FileWriteInput{
			Path:    path,
			Mode:    "write",
			Content: "test content",
		})
		_, writeErr := writeTool.Run(context.Background(), writeRaw)

		// Attempt replace mode
		replaceRaw, _ := json.Marshal(FileWriteInput{
			Path:    path,
			Mode:    "replace",
			OldText: "needle",
			NewText: "replacement",
		})
		_, replaceErr := writeTool.Run(context.Background(), replaceRaw)

		// Attempt patch mode
		patchRaw, _ := json.Marshal(FileWriteInput{
			Path:    path,
			Mode:    "patch",
			Patches: []PatchOperation{{StartLine: 1, EndLine: 1, Content: "patched"}},
		})
		_, patchErr := writeTool.Run(context.Background(), patchRaw)

		// Determine if each mode rejected the path due to sandbox violation
		writeRejected := errors.Is(writeErr, ErrPermissionDenied)
		replaceRejected := errors.Is(replaceErr, ErrPermissionDenied)
		patchRejected := errors.Is(patchErr, ErrPermissionDenied)

		// The key property: all three modes must make the SAME sandbox decision.
		// Either all reject with ErrPermissionDenied, or none do.
		if writeRejected != replaceRejected || writeRejected != patchRejected {
			t.Logf("sandbox invariance violated for path %q:\n  write rejected=%v (err=%v)\n  replace rejected=%v (err=%v)\n  patch rejected=%v (err=%v)",
				path, writeRejected, writeErr, replaceRejected, replaceErr, patchRejected, patchErr)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 500}); err != nil {
		t.Errorf("sandbox invariance property violated: %v", err)
	}
}

// replaceAtomicityInput generates random file content, OldText, and NewText
// for testing replace mode atomicity. The OldText may match zero, one, or
// multiple times in the file content to exercise all code paths.
type replaceAtomicityInput struct {
	Lines   []string // file content lines (1-20 lines of printable ASCII)
	OldText string   // text to search for
	NewText string   // replacement text
}

// Generate implements quick.Generator for replaceAtomicityInput.
func (replaceAtomicityInput) Generate(rng *rand.Rand, size int) reflect.Value {
	// Generate 1-20 lines of printable ASCII content
	numLines := rng.Intn(20) + 1
	lines := make([]string, numLines)
	for i := range lines {
		lineLen := rng.Intn(40) + 1
		buf := make([]byte, lineLen)
		for j := range buf {
			buf[j] = byte(rng.Intn(94) + 32) // printable ASCII
		}
		lines[i] = string(buf)
	}

	content := strings.Join(lines, "\n")

	// Generate OldText using one of three strategies:
	var oldText string
	strategy := rng.Intn(4)
	switch strategy {
	case 0:
		// Strategy: pick a substring that appears exactly once
		// Pick a random position and extract a substring
		if len(content) > 3 {
			start := rng.Intn(len(content) - 2)
			end := start + rng.Intn(min(len(content)-start, 15)) + 1
			oldText = content[start:end]
		} else {
			oldText = content
		}
	case 1:
		// Strategy: generate a random string unlikely to match
		randLen := rng.Intn(10) + 3
		buf := make([]byte, randLen)
		for j := range buf {
			buf[j] = byte(rng.Intn(94) + 32)
		}
		oldText = "ZZUNLIKELY" + string(buf) + "ZZUNLIKELY"
	case 2:
		// Strategy: pick a short string likely to appear multiple times
		chars := []string{"a", "e", "i", " ", "t", "n"}
		oldText = chars[rng.Intn(len(chars))]
	case 3:
		// Strategy: use exact content from a random line (likely unique)
		oldText = lines[rng.Intn(numLines)]
	}

	// Generate a random NewText
	newTextLen := rng.Intn(20)
	newBuf := make([]byte, newTextLen)
	for j := range newBuf {
		newBuf[j] = byte(rng.Intn(94) + 32)
	}
	newText := string(newBuf)

	return reflect.ValueOf(replaceAtomicityInput{
		Lines:   lines,
		OldText: oldText,
		NewText: newText,
	})
}

// **Validates: Requirements FTE-2.1, FTE-2.2, FTE-2.3, FTE-2.7**

// TestProperty_ReplaceAtomicity verifies that a replace operation either modifies
// the file exactly once (when OldText matches uniquely) or leaves the file
// completely unchanged (when the operation fails due to no match or ambiguous match).
func TestProperty_ReplaceAtomicity(t *testing.T) {
	baseDir := t.TempDir()
	baseDir, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		t.Fatalf("failed to resolve baseDir symlinks: %v", err)
	}

	cfg := &SandboxConfig{BaseDirectory: baseDir}
	writeTool := newFileWriteTool(cfg)

	f := func(input replaceAtomicityInput) bool {
		// Skip empty OldText since that's a validation error (tested elsewhere)
		if input.OldText == "" {
			return true
		}

		content := strings.Join(input.Lines, "\n")
		filePath := filepath.Join(baseDir, "test_replace_atomicity.txt")

		// Write the initial file content
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			return false
		}
		defer os.Remove(filePath)

		// Determine expected behavior before calling replace
		matchCount := strings.Count(content, input.OldText)

		// Call replace mode
		raw, _ := json.Marshal(FileWriteInput{
			Path:    filePath,
			Mode:    "replace",
			OldText: input.OldText,
			NewText: input.NewText,
		})
		_, toolErr := writeTool.Run(context.Background(), raw)

		// Read the file content after the operation
		afterData, err := os.ReadFile(filePath)
		if err != nil {
			t.Logf("failed to read file after replace: %v", err)
			return false
		}
		afterContent := string(afterData)

		if toolErr != nil {
			// Operation failed — file MUST be unchanged (FTE-2.7)
			if afterContent != content {
				t.Logf("file was modified despite error (matchCount=%d, err=%v)\nbefore: %q\nafter:  %q",
					matchCount, toolErr, content, afterContent)
				return false
			}

			// Verify the error matches our expectation
			if matchCount == 0 {
				// Expected: ErrNotFound (FTE-2.2)
				if !errors.Is(toolErr, ErrNotFound) {
					t.Logf("expected ErrNotFound for 0 matches, got: %v", toolErr)
					return false
				}
			} else if matchCount > 1 {
				// Expected: ErrValidation for ambiguous match (FTE-2.3)
				if !errors.Is(toolErr, ErrValidation) {
					t.Logf("expected ErrValidation for %d matches, got: %v", matchCount, toolErr)
					return false
				}
			}
			return true
		}

		// Operation succeeded — verify exactly one replacement occurred (FTE-2.1)
		if matchCount != 1 {
			t.Logf("replace succeeded but matchCount was %d (expected 1)", matchCount)
			return false
		}

		// The expected result is the content with exactly one replacement
		expectedContent := strings.Replace(content, input.OldText, input.NewText, 1)
		if afterContent != expectedContent {
			t.Logf("replace result mismatch\nexpected: %q\ngot:      %q", expectedContent, afterContent)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 300}); err != nil {
		t.Errorf("replace atomicity property violated: %v", err)
	}
}

// patchNonOverlapInput generates a random file and a set of non-overlapping patches
// in random order for testing patch mode ordering correctness.
type patchNonOverlapInput struct {
	Lines   []string         // file lines (5-30 lines of printable ASCII)
	Patches []PatchOperation // 1-5 non-overlapping patches in random order
}

// Generate implements quick.Generator for patchNonOverlapInput.
func (patchNonOverlapInput) Generate(rng *rand.Rand, size int) reflect.Value {
	// Generate 5 to 30 lines of random printable ASCII content
	numLines := rng.Intn(26) + 5
	lines := make([]string, numLines)
	for i := range lines {
		lineLen := rng.Intn(40) + 1
		buf := make([]byte, lineLen)
		for j := range buf {
			buf[j] = byte(rng.Intn(94) + 32) // printable ASCII
		}
		lines[i] = string(buf)
	}

	// Generate 1-5 non-overlapping patches with valid line ranges
	numPatches := rng.Intn(5) + 1

	// Strategy: pick random non-overlapping ranges by generating sorted start points
	// with gaps between them.
	type lineRange struct {
		start, end int
	}
	var ranges []lineRange

	// We'll pick ranges by iterating through available line space
	available := numLines
	cursor := 1 // current line position (1-based)

	for i := 0; i < numPatches && cursor <= numLines; i++ {
		// Remaining lines from cursor to end
		remaining := numLines - cursor + 1
		if remaining < 1 {
			break
		}

		// Pick a start within remaining space, leaving room for other patches
		maxStart := remaining
		if maxStart > remaining {
			maxStart = remaining
		}
		// Start offset from cursor: 0 to min(remaining-1, some reasonable gap)
		gap := rng.Intn(min(3, remaining)) // small gap before patch
		start := cursor + gap
		if start > numLines {
			break
		}

		// Pick end: start to start + small range
		maxEnd := min(start+rng.Intn(3), numLines)
		end := start + rng.Intn(maxEnd-start+1)

		ranges = append(ranges, lineRange{start: start, end: end})
		cursor = end + 1 // move past this range

		// Skip at least 0-2 lines between patches to ensure non-overlap
		cursor += rng.Intn(2)

		_ = available
	}

	if len(ranges) == 0 {
		// Ensure at least one patch
		start := rng.Intn(numLines) + 1
		end := start + rng.Intn(min(3, numLines-start+1))
		if end > numLines {
			end = numLines
		}
		ranges = append(ranges, lineRange{start: start, end: end})
	}

	// Build PatchOperations with random replacement content
	patches := make([]PatchOperation, len(ranges))
	for i, r := range ranges {
		// Generate 1-4 replacement lines
		numReplacementLines := rng.Intn(4) + 1
		replacementLines := make([]string, numReplacementLines)
		for j := range replacementLines {
			lineLen := rng.Intn(30) + 1
			buf := make([]byte, lineLen)
			for k := range buf {
				buf[k] = byte(rng.Intn(94) + 32)
			}
			replacementLines[j] = string(buf)
		}
		patches[i] = PatchOperation{
			StartLine: r.start,
			EndLine:   r.end,
			Content:   strings.Join(replacementLines, "\n"),
		}
	}

	// Shuffle patches into random order
	rng.Shuffle(len(patches), func(i, j int) {
		patches[i], patches[j] = patches[j], patches[i]
	})

	return reflect.ValueOf(patchNonOverlapInput{
		Lines:   lines,
		Patches: patches,
	})
}

// **Validates: Requirements FTE-3.2, FTE-3.3**

// TestProperty_PatchNonOverlapOrdering verifies that after sorting patches descending
// by start_line, applying them sequentially does not corrupt earlier patch positions.
// The final file content must match the expected result regardless of input patch order.
func TestProperty_PatchNonOverlapOrdering(t *testing.T) {
	baseDir := t.TempDir()
	baseDir, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		t.Fatalf("failed to resolve baseDir symlinks: %v", err)
	}

	cfg := &SandboxConfig{BaseDirectory: baseDir}
	writeTool := newFileWriteTool(cfg)

	f := func(input patchNonOverlapInput) bool {
		// Write original content to a temp file
		originalContent := strings.Join(input.Lines, "\n")
		filePath := filepath.Join(baseDir, "test_patch_ordering.txt")
		if err := os.WriteFile(filePath, []byte(originalContent), 0644); err != nil {
			return false
		}
		defer os.Remove(filePath)

		// Apply patches via the tool (in random order as generated)
		raw, _ := json.Marshal(FileWriteInput{
			Path:    filePath,
			Mode:    "patch",
			Patches: input.Patches,
		})
		_, err := writeTool.Run(context.Background(), raw)
		if err != nil {
			t.Logf("unexpected error applying patches: %v", err)
			return false
		}

		// Read the actual result from the file
		actualBytes, err := os.ReadFile(filePath)
		if err != nil {
			t.Logf("failed to read result file: %v", err)
			return false
		}
		actualContent := string(actualBytes)

		// Compute expected result by sorting patches descending and applying manually
		sortedPatches := make([]PatchOperation, len(input.Patches))
		copy(sortedPatches, input.Patches)
		sort.Slice(sortedPatches, func(i, j int) bool {
			return sortedPatches[i].StartLine > sortedPatches[j].StartLine
		})

		// Apply sorted patches to the original lines
		expectedLines := make([]string, len(input.Lines))
		copy(expectedLines, input.Lines)

		for _, p := range sortedPatches {
			newLines := strings.Split(p.Content, "\n")
			// Clamp EndLine to totalLines
			endLine := p.EndLine
			if endLine > len(expectedLines) {
				endLine = len(expectedLines)
			}
			before := expectedLines[:p.StartLine-1]
			after := expectedLines[endLine:]
			expectedLines = make([]string, 0, len(before)+len(newLines)+len(after))
			expectedLines = append(expectedLines, before...)
			expectedLines = append(expectedLines, newLines...)
			expectedLines = append(expectedLines, after...)
		}

		expectedContent := strings.Join(expectedLines, "\n")

		if actualContent != expectedContent {
			t.Logf("content mismatch:\nactual:   %q\nexpected: %q\npatches: %+v",
				actualContent, expectedContent, input.Patches)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Errorf("patch non-overlap ordering property violated: %v", err)
	}
}
