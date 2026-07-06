package loader

import (
	"context"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/tools/go/packages"
)

// DefaultMode is the packages.Load mode shared by FindReferences,
// FindDefinition and Rename. Keeping it in one place means cache hits
// are guaranteed to satisfy every caller — if you add a bit here, you
// invalidate the cache semantics for all callers equally.
const DefaultMode = packages.NeedName |
	packages.NeedFiles |
	packages.NeedCompiledGoFiles |
	packages.NeedImports |
	packages.NeedTypes |
	packages.NeedSyntax |
	packages.NeedTypesInfo |
	packages.NeedModule |
	packages.NeedDeps

// ttl bounds how long a cache entry stays fresh after its last use.
// The cache tracks go.mod mtime for correctness; the TTL is a belt-and-
// suspenders measure to prevent a long-running MCP server from holding
// stale types.Info forever in pathological cases (files edited in-place
// without bumping go.mod).
const ttl = 5 * time.Minute

// LoadedPackages bundles the output of packages.Load with its shared
// token.FileSet so callers can translate token.Pos values back to
// file:line:column without re-parsing.
type LoadedPackages struct {
	Fset *token.FileSet
	Pkgs []*packages.Package
}

// Loader wraps packages.Load with a small in-memory cache keyed on
// (absDir, tests). Entries are invalidated when the nearest ancestor
// go.mod's mtime changes, or after 5 minutes of inactivity.
//
// A single Loader is safe to share across handlers; it uses a mutex
// because the workflow is not hot enough to warrant an RWMutex.
type Loader struct {
	mu    sync.Mutex
	cache map[cacheKey]*cacheEntry

	// hits and misses are exposed for tests (and future telemetry)
	// that want to verify cache behaviour without timing assertions.
	hits   atomic.Int64
	misses atomic.Int64
}

type cacheKey struct {
	absDir string
	tests  bool
}

type cacheEntry struct {
	loaded     *LoadedPackages
	goModPath  string    // path used to check mtime; "" means no go.mod found
	goModMTime time.Time // mtime at the time of load
	lastUsed   time.Time
}

// New returns a fresh Loader with an empty cache.
func New() *Loader {
	return &Loader{cache: make(map[cacheKey]*cacheEntry)}
}

// Load returns packages rooted at absDir, reusing a cached result when
// possible. The returned LoadedPackages is shared — callers must not
// mutate it.
//
// Correctness notes:
//   - absDir is the key component, so callers MUST pass an absolute,
//     cleaned path. Passing "." twice from different working dirs
//     would mask a genuine cache miss.
//   - Hard loader errors are not cached; transient problems (e.g. a
//     half-written go.mod during an editor save) should get a fresh
//     attempt on the next call.
//   - Per-package type errors are surfaced to the caller but the
//     result IS cached. The semantics match what the callers had
//     before — they decide whether to elevate the error — and caching
//     avoids a re-load storm during a compile-broken window.
func (l *Loader) Load(ctx context.Context, absDir string, tests bool) (*LoadedPackages, error) {
	key := cacheKey{absDir: absDir, tests: tests}
	now := time.Now()

	l.mu.Lock()
	entry, ok := l.cache[key]
	if ok {
		// Expire stale entries up front so a slow go.mod stat below
		// doesn't extend the TTL for something we were going to drop.
		if now.Sub(entry.lastUsed) > ttl {
			delete(l.cache, key)
			ok = false
			entry = nil
		}
	}
	l.mu.Unlock()

	if ok {
		// mtime check happens outside the lock: stat is cheap but not
		// free, and holding the mutex across it would serialize every
		// cached lookup.
		if fresh, err := isFresh(entry); err == nil && fresh {
			// lastUsed is read under the mutex in the expiry check above —
			// update it under the same lock to keep -race clean.
			l.mu.Lock()
			entry.lastUsed = now
			l.mu.Unlock()
			l.hits.Add(1)
			return entry.loaded, nil
		}
		// Stale: drop and fall through to a fresh load.
		l.mu.Lock()
		delete(l.cache, key)
		l.mu.Unlock()
	}

	l.misses.Add(1)

	fset := token.NewFileSet()
	cfg := &packages.Config{
		Mode:    DefaultMode,
		Context: ctx,
		Dir:     absDir,
		Fset:    fset,
		Tests:   tests,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("load packages from %q: %w", absDir, err)
	}

	loaded := &LoadedPackages{Fset: fset, Pkgs: pkgs}

	goModPath, goModMTime := goModInfo(absDir)
	newEntry := &cacheEntry{
		loaded:     loaded,
		goModPath:  goModPath,
		goModMTime: goModMTime,
		lastUsed:   now,
	}
	l.mu.Lock()
	l.cache[key] = newEntry
	l.mu.Unlock()

	return loaded, nil
}

// Hits returns the cumulative number of cache hits. For tests/telemetry.
func (l *Loader) Hits() int64 { return l.hits.Load() }

// Misses returns the cumulative number of cache misses. For tests/telemetry.
func (l *Loader) Misses() int64 { return l.misses.Load() }

// isFresh reports whether the cached entry still reflects the current
// go.mod. A missing go.mod is treated as "no module boundary to check"
// — in that case we rely on the TTL alone, matching how go/packages
// itself behaves in GOPATH-style layouts.
func isFresh(entry *cacheEntry) (bool, error) {
	if entry.goModPath == "" {
		return true, nil
	}
	st, err := os.Stat(entry.goModPath)
	if err != nil {
		// go.mod vanished since the cache was populated — invalidate.
		return false, err
	}
	return st.ModTime().Equal(entry.goModMTime), nil
}

// goModInfo walks up from absDir looking for the nearest go.mod and
// returns its path + mtime. Returns ("", zero time) when none is found;
// the caller then falls back to the TTL for invalidation.
func goModInfo(absDir string) (string, time.Time) {
	dir := absDir
	for {
		candidate := filepath.Join(dir, "go.mod")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, st.ModTime()
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", time.Time{}
		}
		dir = parent
	}
}

// Invalidate drops every cached entry. The write side calls this after
// files change so queries never serve pre-edit type information.
func (l *Loader) Invalidate() {
	l.mu.Lock()
	l.cache = make(map[cacheKey]*cacheEntry)
	l.mu.Unlock()
}
