package loader_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/loader"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTinyModule scaffolds a one-package Go module so we can
// exercise packages.Load end-to-end without pulling in the project tree.
func writeTinyModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/cache\n\ngo 1.25\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644))
	return dir
}

// TestLoader_CachesSecondCall is the core contract: two back-to-back
// Load calls with the same (absDir, tests) tuple must short-circuit
// to the cached result — otherwise the 800ms-per-load cost is paid
// twice for the typical find_references → rename_symbol workflow.
func TestLoader_CachesSecondCall(t *testing.T) {
	dir := writeTinyModule(t)
	l := loader.New()
	ctx := context.Background()

	first, err := l.Load(ctx, dir, false)
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, int64(0), l.Hits())
	assert.Equal(t, int64(1), l.Misses())

	second, err := l.Load(ctx, dir, false)
	require.NoError(t, err)
	// Identity check: the cache must hand back the SAME pointer, not a
	// re-loaded equivalent — that's how downstream code knows token.Pos
	// values remain valid across calls.
	assert.Same(t, first, second, "second call should return the cached *LoadedPackages")
	assert.Equal(t, int64(1), l.Hits())
	assert.Equal(t, int64(1), l.Misses())
}

// TestLoader_DifferentTestsFlagMisses guards against a key collision:
// Tests=true and Tests=false produce different package sets, so they
// must cache under different keys.
func TestLoader_DifferentTestsFlagMisses(t *testing.T) {
	dir := writeTinyModule(t)
	l := loader.New()
	ctx := context.Background()

	_, err := l.Load(ctx, dir, false)
	require.NoError(t, err)
	_, err = l.Load(ctx, dir, true)
	require.NoError(t, err)

	assert.Equal(t, int64(0), l.Hits())
	assert.Equal(t, int64(2), l.Misses())
}

// TestLoader_InvalidatesOnGoModMTime asserts the correctness half of
// the contract: if go.mod is touched between calls, the cache must
// drop the entry and re-load. Without this, a `go get` during an MCP
// session would leave us serving stale type info.
func TestLoader_InvalidatesOnGoModMTime(t *testing.T) {
	dir := writeTinyModule(t)
	l := loader.New()
	ctx := context.Background()

	_, err := l.Load(ctx, dir, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), l.Misses())

	// Bump go.mod mtime. Adding a full second avoids filesystems with
	// 1s mtime resolution (ext4 with noatime, older HFS+) collapsing
	// the change to a no-op.
	goMod := filepath.Join(dir, "go.mod")
	future := time.Now().Add(2 * time.Second)
	require.NoError(t, os.Chtimes(goMod, future, future))

	_, err = l.Load(ctx, dir, false)
	require.NoError(t, err)
	assert.Equal(t, int64(0), l.Hits(), "mtime change should force a miss")
	assert.Equal(t, int64(2), l.Misses())
}
