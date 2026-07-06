package queries_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/queries"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Item 31: token-budget truncation must leave a visible marker instead of
// silently dropping packages/files/symbols.
func TestGraph_TokenBudget_LeavesTruncationMarker(t *testing.T) {
	tmpDir, fs := setupGraphFixture(t)
	handler := queries.NewSurgeonQueriesHandler(fs)

	// Tiny budget with symbols requested forces the deepest truncation levels
	// (symbols/files/packages dropped).
	packages, err := handler.Graph(context.Background(), domain.GraphOptions{
		Dir:         tmpDir,
		Symbols:     true,
		TokenBudget: 1,
	})
	require.NoError(t, err)
	require.NotEmpty(t, packages)

	var hasMarker bool
	for _, p := range packages {
		if strings.Contains(p.Path, "truncated") && strings.Contains(p.Path, "token_budget") {
			hasMarker = true
		}
	}
	assert.True(t, hasMarker, "expected a visible truncation marker in output, got paths: %v", pkgPaths(packages))
}

func pkgPaths(pkgs []domain.GraphPackage) []string {
	out := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		out = append(out, p.Path)
	}
	return out
}

// Item 36: the recursive walk must skip an unreadable directory instead of
// aborting the whole overview.
func TestGraph_SkipsUnreadableDirectory(t *testing.T) {
	root := t.TempDir()

	goodDir := filepath.Join(root, "good")
	require.NoError(t, os.MkdirAll(goodDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(goodDir, "good.go"),
		[]byte("package good\n\nfunc Good() {}\n"), 0o644))

	badDir := filepath.Join(root, "bad")
	require.NoError(t, os.MkdirAll(badDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(badDir, "bad.go"),
		[]byte("package bad\n\nfunc Bad() {}\n"), 0o644))

	if err := os.Chmod(badDir, 0o000); err != nil {
		t.Skipf("cannot chmod dir unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(badDir, 0o755) })

	// If the process can still read the dir (e.g. running as root), the walk
	// error we want to exercise won't fire — skip rather than assert falsely.
	if _, err := os.ReadDir(badDir); err == nil {
		t.Skip("directory permissions do not restrict this process; cannot exercise walk error")
	}

	fs := &mockFS{files: map[string][]byte{
		filepath.Join(goodDir, "good.go"): []byte("package good\n\nfunc Good() {}\n"),
	}}
	handler := queries.NewSurgeonQueriesHandler(fs)

	packages, err := handler.Graph(context.Background(), domain.GraphOptions{
		Dir:       root,
		Symbols:   true,
		Recursive: true,
	})
	require.NoError(t, err, "walk must not abort on an unreadable directory")

	assert.Contains(t, pkgPaths(packages), goodDir,
		"readable package should still be discovered despite the unreadable sibling")
}
