package loader_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/loader"
	"github.com/stretchr/testify/require"
)

// TestLoader_ConcurrentCacheHits exercises concurrent cache hits on the
// same key. Run with -race: before the fix, entry.lastUsed was written
// outside the mutex on every hit while other goroutines read it under the
// lock, tripping the race detector.
func TestLoader_ConcurrentCacheHits(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module tmpmod\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib.go"), []byte("package tmpmod\n\nfunc Lib() int { return 1 }\n"), 0o644))

	l := loader.New()
	ctx := context.Background()
	_, err := l.Load(ctx, dir, false)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = l.Load(ctx, dir, false)
		}()
	}
	wg.Wait()
	require.GreaterOrEqual(t, l.Hits(), int64(1))
}
