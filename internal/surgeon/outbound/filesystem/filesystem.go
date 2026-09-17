package filesystem

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/imports"
)

// FileSystem is an adapter that interacts with the real file system.
// The captured root anchors path normalization so that writes from a
// server launched in one worktree never silently land in a sibling
// checkout reached via symlink.
type FileSystem struct {
	root string
}

// NewFileSystem creates a new FileSystem adapter and captures the
// worktree root once. Honors the GO_SURGEON_ROOT env var when set;
// falls back to walking up from cwd to a .git entry.
func NewFileSystem() *FileSystem {
	return &FileSystem{root: resolveRoot()}
}

// ReadFile reads the content of the file at path. Reads are allowed
// from anywhere — the worktree guard only protects writes from
// escaping the captured root.
// ReadFile reads the content of the file at path. Relative paths are
// anchored on the captured root so reads resolve the same location writes
// do; absolute paths are honored as-is (reads are allowed from anywhere).
func (f *FileSystem) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return os.ReadFile(f.resolveRead(path))
}

// WriteFile writes content to the file at path. Path is normalized
// against the captured root: relative paths are anchored on root, and
// absolute paths that resolve through a symlink back into the root are
// rewritten to the canonical root prefix. A one-line warning is emitted
// to stderr when a rewrite happens so agents learn the canonical path.
func (f *FileSystem) WriteFile(ctx context.Context, path string, content []byte) ([]string, error) {
	resolved, warning, err := normalizePath(f.root, path)
	if err != nil {
		return nil, err
	}
	if warning != "" {
		fmt.Fprintln(os.Stderr, "go-surgeon: "+warning)
	}
	content, addedImports := applyGoImports(resolved, content)
	if err := os.WriteFile(resolved, content, 0644); err != nil {
		return nil, err
	}
	return addedImports, nil
}

// ReadDir returns the names of the files and directories in path.
// Reads are allowed from anywhere.
// ReadDir returns the names of the files and directories in path.
// Relative paths are anchored on the captured root; absolute paths are
// honored as-is.
func (f *FileSystem) ReadDir(ctx context.Context, path string) ([]string, error) {
	entries, err := os.ReadDir(f.resolveRead(path))
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names, nil
}

// IsDir returns true if the path is a directory. Reads are allowed
// from anywhere.
// IsDir returns true if the path is a directory. Relative paths are
// anchored on the captured root; absolute paths are honored as-is.
func (f *FileSystem) IsDir(ctx context.Context, path string) (bool, error) {
	info, err := os.Stat(f.resolveRead(path))
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

// MkdirAll creates a directory and all necessary parents. Path is
// normalized against the captured root; cross-worktree rewrites are
// warned about on stderr.
func (f *FileSystem) MkdirAll(ctx context.Context, path string) error {
	resolved, warning, err := normalizePath(f.root, path)
	if err != nil {
		return err
	}
	if warning != "" {
		fmt.Fprintln(os.Stderr, "go-surgeon: "+warning)
	}
	if err := os.MkdirAll(resolved, 0755); err != nil {
		return err
	}
	invalidateModuleIndex()
	return nil
}

// DeleteFile removes a file from disk. Path is normalized against the
// captured root; cross-worktree rewrites are warned about on stderr.
func (f *FileSystem) DeleteFile(ctx context.Context, path string) error {
	resolved, warning, err := normalizePath(f.root, path)
	if err != nil {
		return err
	}
	if warning != "" {
		fmt.Fprintln(os.Stderr, "go-surgeon: "+warning)
	}
	if err := os.Remove(resolved); err != nil {
		return err
	}
	invalidateModuleIndex()
	return nil
}

// warnUnresolvedImports parses the Go source and warns to stderr about any
// package-qualified identifiers (e.g. domainerror.New) that have no matching import.
// This catches cases where goimports silently drops unresolvable packages.
func warnUnresolvedImports(path string, src []byte) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return
	}
	for pkg := range collectUnresolvedQualifiers(f) {
		fmt.Fprintf(os.Stderr, "WARNING: goimports could not resolve package %q referenced in %s — you may need to add the import manually.\n", pkg, path)
	}
}

func parseImportPaths(path string, src []byte) map[string]bool {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return nil
	}
	m := make(map[string]bool, len(f.Imports))
	for _, imp := range f.Imports {
		m[strings.Trim(imp.Path.Value, `"`)] = true
	}
	return m
}

// applyGoImports runs goimports on content (if path is a .go file) and
// returns the formatted bytes alongside the list of import paths that
// were added compared to the pre-process content. It also emits
// warnings about unresolved package references to stderr.
//
// Non-.go paths are returned untouched with a nil imports list.
// If goimports fails (e.g. unparseable source), the original content
// and a nil list are returned — the caller should still write.
func applyGoImports(path string, content []byte) ([]byte, []string) {
	if !strings.HasSuffix(path, ".go") {
		return content, nil
	}
	before := parseImportPaths(path, content)

	// Prefer packages of the current module over the global package index:
	// goimports would otherwise happily add an unrelated third-party module
	// (or nothing at all) when a local package shares the qualifier's name.
	localized, localAdded := resolveLocalImports(path, content)

	formatted, err := imports.Process(path, localized, nil)
	if err != nil {
		warnUnresolvedImports(path, localized)
		return localized, localAdded
	}
	after := parseImportPaths(path, formatted)
	var addedImports []string
	for imp := range after {
		if !before[imp] {
			addedImports = append(addedImports, imp)
		}
	}
	warnUnresolvedImports(path, formatted)
	return formatted, addedImports
}

// resolveRead anchors relative paths on the captured root so reads target
// the same location the write-side normalization resolves; absolute paths
// (and rootless mode) pass through untouched.
func (f *FileSystem) resolveRead(path string) string {
	if f.root == "" || path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(f.root, path)
}
