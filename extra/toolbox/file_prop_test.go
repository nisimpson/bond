package toolbox

import (
	"math/rand"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/quick"
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
