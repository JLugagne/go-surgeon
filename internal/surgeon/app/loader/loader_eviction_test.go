package loader

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// White-box tests for the loader cache's bounded-eviction behaviour.
// They live in package loader (not loader_test) so they can seed cache
// entries directly instead of paying a packages.Load for every
// scenario. Each cached entry holds a full type-checked package graph,
// so the cache must never grow without bound in a long-running MCP
// server that queries many directories.

// newFakeEntry returns a cacheEntry with just enough state for eviction
// tests; the loaded payload is never dereferenced by sweepLocked or
// evictLRULocked.
func newFakeEntry(lastUsed time.Time) *cacheEntry {
	return &cacheEntry{lastUsed: lastUsed}
}

// writeTinyModule scaffolds a one-package Go module for the Load-path
// tests in this file.
func writeTinyModule(t *testing.T, modulePath string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module "+modulePath+"\n\ngo 1.25\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644))
	return dir
}

// TestLoader_SweepDropsExpiredEntries asserts the TTL is enforced for
// every entry, not only for the key being looked up: an entry whose
// directory the session moved away from must still be dropped once it
// has been idle for longer than ttl.
func TestLoader_SweepDropsExpiredEntries(t *testing.T) {
	l := newWithCaps(4, 8)
	now := time.Now()
	freshKey := cacheKey{absDir: "/fresh", tests: false}
	staleKey := cacheKey{absDir: "/stale", tests: false}
	l.cache[freshKey] = newFakeEntry(now)
	l.cache[staleKey] = newFakeEntry(now.Add(-ttl - time.Second))

	l.mu.Lock()
	l.sweepLocked(now)
	l.mu.Unlock()

	_, present := l.cache[staleKey]
	assert.False(t, present, "an entry idle for longer than ttl must be dropped")
	_, present = l.cache[freshKey]
	assert.True(t, present, "a recently used entry must survive the sweep")
}

// TestLoader_EvictsLeastRecentlyUsed asserts the size bound picks the
// right victim: the oldest lastUsed entry goes first.
func TestLoader_EvictsLeastRecentlyUsed(t *testing.T) {
	l := newWithCaps(2, 8)
	now := time.Now()
	oldestKey := cacheKey{absDir: "/oldest", tests: false}
	newestKey := cacheKey{absDir: "/newest", tests: false}
	l.cache[oldestKey] = newFakeEntry(now.Add(-time.Minute))
	l.cache[newestKey] = newFakeEntry(now)

	l.mu.Lock()
	l.evictLRULocked(ModeFull)
	l.mu.Unlock()

	_, present := l.cache[oldestKey]
	assert.False(t, present, "the least-recently-used entry must be evicted")
	_, present = l.cache[newestKey]
	assert.True(t, present, "the most recently used entry must survive")
}

// TestLoader_LoadEvictsToMakeRoom proves the cap is enforced on the
// real Load path: with capacity 1, loading a second directory evicts
// the first entry instead of retaining both package graphs.
func TestLoader_LoadEvictsToMakeRoom(t *testing.T) {
	first := writeTinyModule(t, "example.com/first")
	second := writeTinyModule(t, "example.com/second")

	l := newWithCaps(1, 8)
	ctx := context.Background()

	_, err := l.Load(ctx, first, false)
	require.NoError(t, err)

	l.mu.Lock()
	_, cachedFirst := l.cache[cacheKey{absDir: first, tests: false}]
	l.mu.Unlock()
	require.True(t, cachedFirst, "the first load must be cached")

	_, err = l.Load(ctx, second, false)
	require.NoError(t, err)

	l.mu.Lock()
	_, cachedFirst = l.cache[cacheKey{absDir: first, tests: false}]
	_, cachedSecond := l.cache[cacheKey{absDir: second, tests: false}]
	size := len(l.cache)
	l.mu.Unlock()

	assert.False(t, cachedFirst, "loading a new directory at capacity must evict the previous entry")
	assert.True(t, cachedSecond, "the new entry must be cached")
	assert.LessOrEqual(t, size, 1, "the cache must never exceed its capacity")
}

// TestLoader_LoadSweepsExpiredEntries proves the TTL sweep runs on
// every Load, even when the requested key is unrelated to the stale
// entry.
func TestLoader_LoadSweepsExpiredEntries(t *testing.T) {
	dir := writeTinyModule(t, "example.com/live")

	l := newWithCaps(4, 8)
	staleKey := cacheKey{absDir: "/abandoned", tests: false}
	l.cache[staleKey] = newFakeEntry(time.Now().Add(-ttl - time.Second))

	_, err := l.Load(context.Background(), dir, false)
	require.NoError(t, err)

	l.mu.Lock()
	_, present := l.cache[staleKey]
	l.mu.Unlock()
	assert.False(t, present, "Load must sweep entries that outlived the TTL")
}

// TestLoader_FullAndGraphEntriesCoexist asserts the load mode is part of
// the cache key: a graph load must not be served from a full entry (it
// lacks the syntax and types its callers never asked for), and each
// mode's entry stays cached across a load of the other mode.
func TestLoader_FullAndGraphEntriesCoexist(t *testing.T) {
	dir := writeTinyModule(t, "example.com/modes")
	l := newWithCaps(4, 8)
	ctx := context.Background()

	full, err := l.Load(ctx, dir, false)
	require.NoError(t, err)
	graph, err := l.LoadGraph(ctx, dir, false)
	require.NoError(t, err)
	require.NotSame(t, full, graph, "a graph load must not reuse a full entry")

	againFull, err := l.Load(ctx, dir, false)
	require.NoError(t, err)
	assert.Same(t, full, againFull, "the full entry must still be cached")

	againGraph, err := l.LoadGraph(ctx, dir, false)
	require.NoError(t, err)
	assert.Same(t, graph, againGraph, "the graph entry must still be cached")
}

// TestLoader_GraphLoadDoesNotEvictFullEntry proves the caps are enforced
// per mode: a cheap structural entry must never displace an expensive
// full graph, while a full load at capacity still evicts the oldest full
// entry.
func TestLoader_GraphLoadDoesNotEvictFullEntry(t *testing.T) {
	first := writeTinyModule(t, "example.com/first")
	second := writeTinyModule(t, "example.com/second")
	third := writeTinyModule(t, "example.com/third")

	l := newWithCaps(1, 1)
	ctx := context.Background()

	_, err := l.Load(ctx, first, false)
	require.NoError(t, err)
	_, err = l.LoadGraph(ctx, second, false)
	require.NoError(t, err)

	l.mu.Lock()
	_, fullPresent := l.cache[cacheKey{absDir: first, tests: false, mode: ModeFull}]
	_, graphPresent := l.cache[cacheKey{absDir: second, tests: false, mode: ModeGraph}]
	l.mu.Unlock()
	assert.True(t, fullPresent, "a graph load must not evict a full entry")
	assert.True(t, graphPresent, "the graph entry must be cached")

	_, err = l.Load(ctx, third, false)
	require.NoError(t, err)

	l.mu.Lock()
	_, fullPresent = l.cache[cacheKey{absDir: first, tests: false, mode: ModeFull}]
	_, graphPresent = l.cache[cacheKey{absDir: second, tests: false, mode: ModeGraph}]
	l.mu.Unlock()
	assert.False(t, fullPresent, "the full cap must still evict the oldest full entry")
	assert.True(t, graphPresent, "graph entries survive full-graph eviction")
}
