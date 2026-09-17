package loader

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
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

// GraphMode is the lightweight packages.Load mode used for structural
// dependency walks (the affected-packages closure behind build_check and
// test_run). Omitting NeedSyntax, NeedTypes, NeedTypesInfo and NeedDeps
// keeps Go from parsing or type-checking source: measured on this
// repository, a full load retains ~550 MB of live heap while a graph
// load retains ~1 MB.
const GraphMode = packages.NeedName |
	packages.NeedFiles |
	packages.NeedCompiledGoFiles |
	packages.NeedImports |
	packages.NeedModule

// maxGraphEntries bounds the lightweight structural loads produced by
// LoadGraph. A graph entry keeps names, files, imports and module paths
// only — orders of magnitude smaller than a full entry — so a larger
// cap is cheap and avoids re-running `go list` during reverse-
// dependency walks.
const maxGraphEntries = 8

// ttl bounds how long a cache entry stays fresh after its last use.
// The cache tracks go.mod mtime for correctness; the TTL is a belt-and-
// suspenders measure to prevent a long-running MCP server from holding
// stale types.Info forever in pathological cases (files edited in-place
// without bumping go.mod).
//
// maxEntries bounds how many (absDir, tests) results the cache retains.
// Each entry holds a full type-checked package graph, which can reach
// hundreds of megabytes on a large module. Without a bound, a long-
// running MCP server that queries many distinct directories would keep
// every graph alive forever, so the least-recently-used entry is dropped
// once the cap is reached.
const (
	ttl        = 5 * time.Minute
	maxEntries = 2
)

// LoadedPackages bundles the output of packages.Load with its shared
// token.FileSet so callers can translate token.Pos values back to
// file:line:column without re-parsing.
type LoadedPackages struct {
	Fset *token.FileSet
	Pkgs []*packages.Package
}

// Loader wraps packages.Load with a bounded in-memory cache keyed on
// (absDir, tests, mode). Entries are invalidated when the nearest ancestor
// go.mod's mtime changes, or after 5 minutes of inactivity. Stale entries
// are swept on every load, and each mode evicts its own least-recently-used
// entry once its cap is reached (maxEntries for full graphs,
// maxGraphEntries for structural ones), so a long-running process cannot
// accumulate package graphs without bound.
type Loader struct {
	mu            sync.Mutex
	cache         map[cacheKey]*cacheEntry
	fullCapacity  int
	graphCapacity int

	// hits and misses are exposed for tests (and future telemetry)
	// that want to verify cache behaviour without timing assertions.
	hits   atomic.Int64
	misses atomic.Int64
}

type cacheKey struct {
	absDir string
	tests  bool
	mode   LoadMode
}

type cacheEntry struct {
	loaded     *LoadedPackages
	goModPath  string    // path used to check mtime; "" means no go.mod found
	goModMTime time.Time // mtime at the time of load
	lastUsed   time.Time
}

// New returns a fresh Loader with an empty cache.
func New() *Loader {
	return newWithCaps(maxEntries, maxGraphEntries)
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

// sweepLocked drops every entry idle for longer than ttl. It must be
// called with l.mu held. Sweeping on each Load — instead of only when
// the requested key is revisited — is what stops the cache from
// retaining package graphs for directories the session moved away from.
func (l *Loader) sweepLocked(now time.Time) {
	for k, e := range l.cache {
		if now.Sub(e.lastUsed) > ttl {
			delete(l.cache, k)
		}
	}
}

// evictLRULocked drops the least-recently-used entry of the given mode to
// make room for a new one. Scoping eviction to a mode keeps a cheap graph
// load from displacing an expensive full graph. It must be called with
// l.mu held.
func (l *Loader) evictLRULocked(mode LoadMode) {
	var oldestKey cacheKey
	var oldest *cacheEntry
	for k, e := range l.cache {
		if k.mode != mode {
			continue
		}
		if oldest == nil || e.lastUsed.Before(oldest.lastUsed) {
			oldestKey, oldest = k, e
		}
	}
	if oldest != nil {
		delete(l.cache, oldestKey)
	}
}

// Load returns packages rooted at absDir with full syntax and type
// information, reusing a cached result when possible. The returned
// LoadedPackages is shared — callers must not mutate it.
//
// Dependency packages pulled in by NeedDeps have their Syntax and
// TypesInfo released before the graph is cached: callers only inspect the
// root packages, and the dependency payloads account for most of the
// retained heap. Dependency types remain reachable through the roots.
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
	return l.load(ctx, cacheKey{absDir: absDir, tests: tests, mode: ModeFull})
}

// LoadMode identifies which packages.Load bit set a cache entry was
// produced with. It is part of the cache key: a structural graph entry
// must never satisfy a caller that needs types and syntax, and the two
// kinds are bounded independently so a cheap graph load cannot evict an
// expensive full graph.
type LoadMode int

const (
	// ModeFull retains syntax and type information (DefaultMode).
	ModeFull LoadMode = iota
	// ModeGraph retains only package structure (GraphMode).
	ModeGraph
)

// newWithCaps returns a Loader whose cache holds at most fullCapacity
// full graphs and graphCapacity structural graphs. Non-positive
// capacities fall back to the defaults so the bounds can never be
// disabled by accident.
func newWithCaps(fullCapacity, graphCapacity int) *Loader {
	if fullCapacity <= 0 {
		fullCapacity = maxEntries
	}
	if graphCapacity <= 0 {
		graphCapacity = maxGraphEntries
	}
	return &Loader{
		cache:         make(map[cacheKey]*cacheEntry),
		fullCapacity:  fullCapacity,
		graphCapacity: graphCapacity,
	}
}

// LoadGraph is the structural counterpart of Load: it resolves package
// names, files, imports and module paths without parsing or type-checking
// source. Use it for dependency walks such as the affected-packages
// closure; graph entries are tiny compared to full entries and are
// cached under a separate, larger cap.
func (l *Loader) LoadGraph(ctx context.Context, absDir string, tests bool) (*LoadedPackages, error) {
	return l.load(ctx, cacheKey{absDir: absDir, tests: tests, mode: ModeGraph})
}

// load implements Load and LoadGraph: cache lookup, freshness check and
// insertion with per-mode eviction.
func (l *Loader) load(ctx context.Context, key cacheKey) (*LoadedPackages, error) {
	now := time.Now()

	l.mu.Lock()
	l.sweepLocked(now)
	entry, ok := l.cache[key]
	l.mu.Unlock()

	if ok {
		// mtime check happens outside the lock: stat is cheap but not
		// free, and holding the mutex across it would serialize every
		// cached lookup.
		if fresh, err := isFresh(entry); err == nil && fresh {
			// lastUsed is read under the mutex by the sweep above —
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
		Mode:      key.mode.packagesMode(),
		Context:   ctx,
		Dir:       key.absDir,
		Fset:      fset,
		Tests:     key.tests,
		ParseFile: parseWithoutObjectResolution,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("load packages from %q: %w", key.absDir, err)
	}

	if key.mode == ModeFull {
		releaseDependencyPayloads(pkgs)
	}

	loaded := &LoadedPackages{Fset: fset, Pkgs: pkgs}

	goModPath, goModMTime := goModInfo(key.absDir)
	newEntry := &cacheEntry{
		loaded:     loaded,
		goModPath:  goModPath,
		goModMTime: goModMTime,
		lastUsed:   now,
	}
	l.mu.Lock()
	l.sweepLocked(now)
	capacity := l.capacityFor(key.mode)
	for l.modeCountLocked(key.mode) >= capacity {
		l.evictLRULocked(key.mode)
	}
	l.cache[key] = newEntry
	l.mu.Unlock()

	return loaded, nil
}

// capacityFor returns the cache capacity for a mode, falling back to
// the defaults for zero-value Loaders built by struct literal. A
// capacity of zero would otherwise make the eviction loop spin without
// ever making room.
func (l *Loader) capacityFor(mode LoadMode) int {
	if mode == ModeGraph {
		if l.graphCapacity > 0 {
			return l.graphCapacity
		}
		return maxGraphEntries
	}
	if l.fullCapacity > 0 {
		return l.fullCapacity
	}
	return maxEntries
}

// modeCountLocked returns how many cached entries use the given mode.
// It must be called with l.mu held.
func (l *Loader) modeCountLocked(mode LoadMode) int {
	n := 0
	for k := range l.cache {
		if k.mode == mode {
			n++
		}
	}
	return n
}

// packagesMode maps a cache LoadMode to the packages.Load bit set.
func (m LoadMode) packagesMode() packages.LoadMode {
	if m == ModeGraph {
		return GraphMode
	}
	return DefaultMode
}

// parseWithoutObjectResolution parses with the same flags go/packages
// uses (AllErrors|ParseComments) plus SkipObjectResolution. go/packages
// keeps deprecated ast.Object resolution for compatibility, but every
// go-surgeon caller resolves identity through go/types.Info, so the
// object graph and its scopes are pure dead weight in the cache.
func parseWithoutObjectResolution(fset *token.FileSet, filename string, src []byte) (*ast.File, error) {
	return parser.ParseFile(fset, filename, src, parser.AllErrors|parser.ParseComments|parser.SkipObjectResolution)
}

// releaseDependencyPayloads frees the syntax trees and types.Info tables
// of every transitive dependency package. Only the root packages returned
// by Load are ever read by the loader's consumers (find_references,
// find_definition and rename iterate loaded.Pkgs and the roots' type
// info); dependency types stay reachable through the roots' types
// packages, but their parsed ASTs and per-file type info are dead weight.
// Without this, NeedDeps makes go/packages parse and type-check the
// entire dependency graph from source: measured on this repository,
// releasing these fields cuts a cached full load from ~530 MB to ~100 MB
// of live heap (2,314 dependency syntax files versus 90 root ones).
func releaseDependencyPayloads(roots []*packages.Package) {
	rootSet := make(map[*packages.Package]struct{}, len(roots))
	for _, p := range roots {
		rootSet[p] = struct{}{}
	}
	seen := make(map[*packages.Package]struct{}, len(roots)*4)
	var walk func(p *packages.Package)
	walk = func(p *packages.Package) {
		if p == nil {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		for _, imp := range p.Imports {
			walk(imp)
		}
		if _, isRoot := rootSet[p]; isRoot {
			return
		}
		p.Syntax = nil
		p.TypesInfo = nil
	}
	for _, p := range roots {
		walk(p)
	}
}
