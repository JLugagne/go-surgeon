package queries_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/queries"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/outbound/filesystem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realHandler returns a SurgeonQueriesHandler backed by the real filesystem.
// Integration tests use this because module resolution via packages.Load shells
// out to go list, and subsequent file reads must reach the module cache on disk.
func realHandler(t *testing.T) *queries.SurgeonQueriesHandler {
	t.Helper()
	return queries.NewSurgeonQueriesHandler(filesystem.NewFileSystem())
}

// TestGraph_WithModule_ListsCobraPackages verifies that passing Module resolves
// the dependency to its on-disk source, walks it, and returns relative paths
// prefixed with a module header entry.
func TestGraph_WithModule_ListsCobraPackages(t *testing.T) {
	handler := realHandler(t)

	pkgs, err := handler.Graph(context.Background(), domain.GraphOptions{
		Module: "github.com/spf13/cobra",
	})
	require.NoError(t, err)
	require.NotEmpty(t, pkgs)

	// First entry must be the module header comment.
	assert.True(t, strings.HasPrefix(pkgs[0].Path, "# Module:"),
		"first entry should be module header, got %q", pkgs[0].Path)
	assert.Contains(t, pkgs[0].Path, "github.com/spf13/cobra")

	// Remaining entries are package paths relative to the module root.
	for _, p := range pkgs[1:] {
		assert.False(t, filepath.IsAbs(p.Path),
			"package path %q should be relative to module root", p.Path)
		assert.NotContains(t, p.Path, "@",
			"package path %q should not contain a version marker", p.Path)
	}
}

// TestGraph_WithModule_AndDir_ScopesSubPackage verifies that combining --module
// with a relative --dir restricts the walk to that sub-directory.
func TestGraph_WithModule_AndDir_ScopesSubPackage(t *testing.T) {
	handler := realHandler(t)

	// cobra has a "doc" sub-package with its own .go files.
	pkgs, err := handler.Graph(context.Background(), domain.GraphOptions{
		Module:  "github.com/spf13/cobra",
		Dir:     "doc",
		Symbols: true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, pkgs)

	// The header entry is always present when Module is set.
	assert.True(t, strings.HasPrefix(pkgs[0].Path, "# Module:"),
		"first entry should be module header")

	// All non-header package paths must live under the "doc" sub-directory.
	for _, p := range pkgs[1:] {
		assert.True(t,
			p.Path == "doc" || strings.HasPrefix(p.Path, "doc/"),
			"expected path inside doc/, got %q", p.Path)
	}
}

// TestGraph_WithModule_InvalidModule_ReturnsError verifies that a module not
// present in go.mod produces a descriptive error rather than a panic or empty result.
func TestGraph_WithModule_InvalidModule_ReturnsError(t *testing.T) {
	handler := realHandler(t)

	_, err := handler.Graph(context.Background(), domain.GraphOptions{
		Module: "github.com/this/does/not/exist/in/gomod",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a dependency",
		"error should mention that the module is not a dependency")
}

// TestFindSymbols_WithModule_FindsCobraSymbol verifies that FindSymbols with a
// Module set locates the Command struct inside cobra and returns a relative file path.
func TestFindSymbols_WithModule_FindsCobraSymbol(t *testing.T) {
	handler := realHandler(t)

	results, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{
		Name:   "Command",
		Module: "github.com/spf13/cobra",
	}, ".")
	require.NoError(t, err)
	require.NotEmpty(t, results)

	found := false
	for _, r := range results {
		if r.Name == "Command" {
			found = true
			assert.False(t, filepath.IsAbs(r.File),
				"file path %q should be relative to module root", r.File)
			assert.NotContains(t, r.File, "@",
				"file path %q should not contain a version marker", r.File)
			break
		}
	}
	assert.True(t, found, "expected to find the Command struct in cobra")
}

// TestGraph_WithModule_CachesResults verifies that two identical module lookups
// return the same number of packages (the second call hits the in-process cache).
func TestGraph_WithModule_CachesResults(t *testing.T) {
	handler := realHandler(t)

	opts := domain.GraphOptions{
		Module: "github.com/spf13/cobra",
		Depth:  1,
	}

	pkgs1, err := handler.Graph(context.Background(), opts)
	require.NoError(t, err)

	pkgs2, err := handler.Graph(context.Background(), opts)
	require.NoError(t, err)

	assert.Equal(t, len(pkgs1), len(pkgs2),
		"cached result should produce the same package count")
}

// TestGraph_WithoutModule_BehaviorUnchanged verifies that omitting --module keeps
// the existing walk-from-dir behavior and does not add a module header.
func TestGraph_WithoutModule_BehaviorUnchanged(t *testing.T) {
	handler := realHandler(t)

	tmpDir := t.TempDir()
	pkgDir := filepath.Join(tmpDir, "pkg")
	require.NoError(t, os.MkdirAll(pkgDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(pkgDir, "foo.go"),
		[]byte("package pkg\nfunc Foo() {}\n"),
		0644,
	))

	pkgs, err := handler.Graph(context.Background(), domain.GraphOptions{
		Dir: pkgDir,
	})
	require.NoError(t, err)
	require.Len(t, pkgs, 1)

	// No module header should be prepended when Module is not set.
	assert.False(t, strings.HasPrefix(pkgs[0].Path, "#"),
		"no module header expected when Module is empty, got %q", pkgs[0].Path)
}

// TestGraph_WithModule_AbsoluteDir_ReturnsError verifies that passing an absolute
// path to --dir together with --module is rejected with a clear error.
func TestGraph_WithModule_AbsoluteDir_ReturnsError(t *testing.T) {
	handler := realHandler(t)

	_, err := handler.Graph(context.Background(), domain.GraphOptions{
		Module: "github.com/spf13/cobra",
		Dir:    "/tmp/absolute",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "relative",
		"error should mention that --dir must be a relative path when --module is set")
}
