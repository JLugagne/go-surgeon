package commands

import (
	"context"
	"sort"
	"sync"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// fileLocks is a reference-counted keyed mutex set. Commands acquire the
// lock(s) of every file they read-modify-write so concurrent tool calls
// targeting the same path serialize instead of racing to a
// last-writer-wins outcome that still reports SUCCESS (issue #33).
//
// Keys are always locked in sorted order and released in reverse order,
// keeping multi-file acquisitions deadlock-free as long as every caller
// goes through acquire.
type fileLocks struct {
	mu    sync.Mutex
	locks map[string]*refLock
}

// refLock is a mutex plus the number of callers currently holding or
// waiting for it, so the map entry is only dropped once nobody needs it.
type refLock struct {
	mu   sync.Mutex
	refs int
}

func newFileLocks() *fileLocks {
	return &fileLocks{locks: make(map[string]*refLock)}
}

// globalFileLocks backs handlers constructed without NewExecutePlanHandler
// (zero-value structs in tests or embedders) so serialization is never
// silently disabled.
var globalFileLocks = newFileLocks()

// acquire locks every distinct key and returns a release function.
func (l *fileLocks) acquire(keys ...string) func() {
	uniq := normalizeLockKeys(keys)
	if len(uniq) == 0 {
		return func() {}
	}

	l.mu.Lock()
	held := make([]*refLock, 0, len(uniq))
	for _, k := range uniq {
		rl := l.locks[k]
		if rl == nil {
			rl = &refLock{}
			l.locks[k] = rl
		}
		rl.refs++
		held = append(held, rl)
	}
	l.mu.Unlock()

	for _, rl := range held {
		rl.mu.Lock()
	}

	return func() {
		l.mu.Lock()
		for i, k := range uniq {
			held[i].refs--
			if held[i].refs == 0 {
				delete(l.locks, k)
			}
		}
		l.mu.Unlock()
		for i := len(held) - 1; i >= 0; i-- {
			held[i].mu.Unlock()
		}
	}
}

func normalizeLockKeys(keys []string) []string {
	seen := make(map[string]bool, len(keys))
	uniq := make([]string, 0, len(keys))
	for _, k := range keys {
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		uniq = append(uniq, k)
	}
	sort.Strings(uniq)
	return uniq
}

// heldLocksKey carries the paths already locked by an enclosing handler
// call through the context, making acquisition reentrant across nested
// calls (execute_plan -> PatchFunction) without re-locking.
type heldLocksKey struct{}

func heldLocks(ctx context.Context) map[string]bool {
	if v, ok := ctx.Value(heldLocksKey{}).(map[string]bool); ok {
		return v
	}
	return nil
}

// lockFiles serializes the current operation against other operations on
// the same paths. It returns a derived context that carries the enlarged
// lock set plus a release function.
func (h *ExecutePlanHandler) lockFiles(ctx context.Context, paths ...string) (context.Context, func()) {
	held := heldLocks(ctx)
	var toAcquire []string
	for _, p := range paths {
		if p == "" || (held != nil && held[p]) {
			continue
		}
		toAcquire = append(toAcquire, p)
	}
	toAcquire = normalizeLockKeys(toAcquire)
	if len(toAcquire) == 0 {
		return ctx, func() {}
	}
	release := h.fileLockSet().acquire(toAcquire...)
	merged := make(map[string]bool, len(held)+len(toAcquire))
	for k := range held {
		merged[k] = true
	}
	for _, p := range toAcquire {
		merged[p] = true
	}
	return context.WithValue(ctx, heldLocksKey{}, merged), release
}

func (h *ExecutePlanHandler) fileLockSet() *fileLocks {
	if h.locks != nil {
		return h.locks
	}
	return globalFileLocks
}

// planLockPaths returns every file a plan may write, so Handle can hold
// their locks for the whole read-modify-commit sequence.
func planLockPaths(plan domain.Plan) []string {
	var paths []string
	for _, a := range plan.Actions {
		paths = append(paths, a.FilePath, a.MockFile)
	}
	return paths
}
