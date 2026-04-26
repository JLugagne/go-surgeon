package filesystem

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
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
func (f *FileSystem) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}

// WriteFile writes content to the file at path. Path is normalized
// against the captured root: relative paths are anchored on root, and
// absolute paths that resolve through a symlink into a sibling worktree
// are rewritten to land inside our root. A one-line warning is emitted
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
func (f *FileSystem) ReadDir(ctx context.Context, path string) ([]string, error) {
	entries, err := os.ReadDir(path)
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
func (f *FileSystem) IsDir(ctx context.Context, path string) (bool, error) {
	info, err := os.Stat(path)
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
	return os.MkdirAll(resolved, 0755)
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
	return os.Remove(resolved)
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

	imported := make(map[string]bool)
	for _, imp := range f.Imports {
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		} else {
			p := strings.Trim(imp.Path.Value, `"`)
			parts := strings.Split(p, "/")
			name = parts[len(parts)-1]
		}
		imported[name] = true
	}

	// Collect all locally declared identifiers so we don't mistake variable names
	// (e.g. "sc", "cmdBuf") for unresolved package names.
	declared := make(map[string]bool)
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			if v.Tok == token.DEFINE {
				for _, lhs := range v.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						declared[id.Name] = true
					}
				}
			}
		case *ast.ValueSpec:
			for _, name := range v.Names {
				declared[name.Name] = true
			}
		case *ast.Field:
			for _, name := range v.Names {
				declared[name.Name] = true
			}
		case *ast.RangeStmt:
			if id, ok := v.Key.(*ast.Ident); ok {
				declared[id.Name] = true
			}
			if v.Value != nil {
				if id, ok := v.Value.(*ast.Ident); ok {
					declared[id.Name] = true
				}
			}
		case *ast.TypeSpec:
			declared[v.Name.Name] = true
		case *ast.FuncDecl:
			if v.Name != nil {
				declared[v.Name.Name] = true
			}
		}
		return true
	})

	// Collect package-qualified identifiers not backed by an import.
	unresolved := make(map[string]bool)
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		pkg := ident.Name
		if !imported[pkg] && pkg != f.Name.Name && !declared[pkg] {
			unresolved[pkg] = true
		}
		return true
	})

	for pkg := range unresolved {
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
	formatted, err := imports.Process(path, content, nil)
	if err != nil {
		warnUnresolvedImports(path, content)
		return content, nil
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
