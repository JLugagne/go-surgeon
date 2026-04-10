package queries

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/tools/go/packages"
)

// ModuleInfo describes a resolved Go module.
type ModuleInfo struct {
	Path    string // e.g. "github.com/spf13/cobra"
	Version string // e.g. "v1.10.2"
	Dir     string // absolute path to module root on disk
}

// moduleResolver caches import path → ModuleInfo lookups for the lifetime of a process.
type moduleResolver struct {
	mu    sync.Mutex
	cache map[string]*ModuleInfo
}

func newModuleResolver() *moduleResolver {
	return &moduleResolver{cache: make(map[string]*ModuleInfo)}
}

// Resolve looks up a module by import path and returns its root directory on disk.
// The lookup is performed from the current working directory, so the project's
// go.mod determines which version is selected (handles replace directives and vendoring).
func (r *moduleResolver) Resolve(ctx context.Context, importPath string) (*ModuleInfo, error) {
	r.mu.Lock()
	if info, ok := r.cache[importPath]; ok {
		r.mu.Unlock()
		return info, nil
	}
	r.mu.Unlock()

	cfg := &packages.Config{
		Mode:    packages.NeedModule | packages.NeedName,
		Context: ctx,
		Dir:     ".",
	}
	pkgs, err := packages.Load(cfg, importPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve module %q: %w", importPath, err)
	}
	if len(pkgs) == 0 || pkgs[0].Module == nil {
		return nil, fmt.Errorf("module %q is not a dependency of this project; check go.mod or run 'go list -m all' to see available modules", importPath)
	}

	mod := pkgs[0].Module
	if mod.Dir == "" {
		return nil, fmt.Errorf("could not determine source directory for module %q; try running 'go mod download'", importPath)
	}

	info := &ModuleInfo{
		Path:    mod.Path,
		Version: mod.Version,
		Dir:     mod.Dir,
	}

	r.mu.Lock()
	r.cache[importPath] = info
	r.cache[mod.Path] = info // also cache under the canonical module root path
	r.mu.Unlock()

	return info, nil
}
