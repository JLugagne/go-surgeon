package filesystem

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/tools/imports"
)

// FileSystem is an adapter that interacts with the real file system.
type FileSystem struct{}

// NewFileSystem creates a new FileSystem adapter.
func NewFileSystem() *FileSystem {
	return &FileSystem{}
}

// ReadFile reads the content of the file at path.
func (f *FileSystem) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}

// WriteFile writes content to the file at path.
func (f *FileSystem) WriteFile(ctx context.Context, path string, content []byte) error {
	if strings.HasSuffix(path, ".go") {
		formatted, err := imports.Process(path, content, nil)
		if err == nil {
			content = formatted
		}
		warnUnresolvedImports(path, content)
	}
	return os.WriteFile(path, content, 0644)
}

// ReadDir returns the names of the files and directories in path.
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

// IsDir returns true if the path is a directory.
func (f *FileSystem) IsDir(ctx context.Context, path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

// MkdirAll creates a directory and all necessary parents.
func (f *FileSystem) MkdirAll(ctx context.Context, path string) error {
	return os.MkdirAll(path, 0755)
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

// ExecuteGoImports executes goimports -w on the provided files.
func (f *FileSystem) ExecuteGoImports(ctx context.Context, files []string) error {
	if len(files) == 0 {
		return nil
	}

	args := append([]string{"-w"}, files...)
	cmd := exec.CommandContext(ctx, "goimports", args...)
	return cmd.Run()
}
