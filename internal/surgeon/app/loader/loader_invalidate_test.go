package loader_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/loader"
	"github.com/stretchr/testify/require"
)

// TestLoader_InvalidateForcesReload asserts Invalidate drops cached entries
// so the next Load re-runs packages.Load instead of serving pre-edit type
// information.
func TestLoader_InvalidateForcesReload(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module tmpmod\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib.go"), []byte("package tmpmod\n\nfunc Lib() int { return 1 }\n"), 0o644))

	l := loader.New()
	ctx := context.Background()

	_, err := l.Load(ctx, dir, false)
	require.NoError(t, err)
	_, err = l.Load(ctx, dir, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), l.Hits(), "second load must hit the cache")

	l.Invalidate()

	_, err = l.Load(ctx, dir, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), l.Misses(), "a load after Invalidate must re-run packages.Load")
}
