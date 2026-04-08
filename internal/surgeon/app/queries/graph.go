package queries

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// Graph walks the directory tree and returns a structural map of Go packages.
// When symbols is true, each package includes its files with exported symbol signatures.
func (h *SurgeonQueriesHandler) Graph(ctx context.Context, dir string, symbols bool) ([]domain.GraphPackage, error) {
	packageFiles := make(map[string][]string)

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if name == "vendor" || (strings.HasPrefix(name, ".") && path != dir) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		dirPath := filepath.Dir(path)
		packageFiles[dirPath] = append(packageFiles[dirPath], path)
		return nil
	})
	if err != nil {
		return nil, err
	}

	var pkgPaths []string
	for p := range packageFiles {
		pkgPaths = append(pkgPaths, p)
	}
	sort.Strings(pkgPaths)

	var packages []domain.GraphPackage
	for _, pkgPath := range pkgPaths {
		pkg := domain.GraphPackage{Path: pkgPath}

		if symbols {
			files := packageFiles[pkgPath]
			sort.Strings(files)

			for _, filePath := range files {
				gf, err := h.extractGraphSymbols(ctx, filePath)
				if err != nil {
					continue
				}
				if len(gf.Symbols) > 0 {
					pkg.Files = append(pkg.Files, gf)
				}
			}
		}

		packages = append(packages, pkg)
	}

	return packages, nil
}

func (h *SurgeonQueriesHandler) extractGraphSymbols(ctx context.Context, path string) (domain.GraphFile, error) {
	src, err := h.fs.ReadFile(ctx, path)
	if err != nil {
		return domain.GraphFile{}, err
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return domain.GraphFile{}, err
	}

	var symbols []string

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			sig := formatFuncSig(src, fset, d)
			if sig != "" {
				symbols = append(symbols, sig)
			}
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				sym := formatTypeSig(src, fset, ts)
				if sym != "" {
					symbols = append(symbols, sym)
				}
			}
		}
	}

	return domain.GraphFile{
		Path:    path,
		Symbols: symbols,
	}, nil
}

func formatFuncSig(src []byte, fset *token.FileSet, fn *ast.FuncDecl) string {
	start := fset.Position(fn.Pos()).Offset
	if fn.Body != nil {
		bodyStart := fset.Position(fn.Body.Lbrace).Offset
		return strings.TrimSpace(string(src[start:bodyStart]))
	}
	end := fset.Position(fn.End()).Offset
	return strings.TrimSpace(string(src[start:end]))
}

func formatTypeSig(src []byte, fset *token.FileSet, ts *ast.TypeSpec) string {
	switch t := ts.Type.(type) {
	case *ast.StructType:
		return formatStructSig(src, fset, ts.Name.Name, t)
	case *ast.InterfaceType:
		return formatInterfaceSig(src, fset, ts.Name.Name, t)
	default:
		typeStr := nodeSource(src, fset, ts.Type)
		return fmt.Sprintf("type %s %s", ts.Name.Name, typeStr)
	}
}

func formatStructSig(src []byte, fset *token.FileSet, name string, st *ast.StructType) string {
	if st.Fields == nil || len(st.Fields.List) == 0 {
		return fmt.Sprintf("type %s struct {}", name)
	}

	var parts []string
	for _, field := range st.Fields.List {
		typeStr := nodeSource(src, fset, field.Type)
		if len(field.Names) == 0 {
			parts = append(parts, typeStr)
		} else {
			for _, n := range field.Names {
				parts = append(parts, n.Name+" "+typeStr)
			}
		}
	}

	if len(parts) <= 5 {
		return fmt.Sprintf("type %s struct { %s }", name, strings.Join(parts, "; "))
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "type %s struct {\n", name)
	for _, p := range parts {
		fmt.Fprintf(&buf, "    %s\n", p)
	}
	buf.WriteString("}")
	return buf.String()
}

func formatInterfaceSig(src []byte, fset *token.FileSet, name string, it *ast.InterfaceType) string {
	if it.Methods == nil || len(it.Methods.List) == 0 {
		return fmt.Sprintf("type %s interface {}", name)
	}

	var names []string
	for _, m := range it.Methods.List {
		if len(m.Names) > 0 {
			names = append(names, m.Names[0].Name)
		} else {
			names = append(names, nodeSource(src, fset, m.Type))
		}
	}

	return fmt.Sprintf("type %s interface { %s }", name, strings.Join(names, "; "))
}

func nodeSource(src []byte, fset *token.FileSet, node ast.Node) string {
	start := fset.Position(node.Pos()).Offset
	end := fset.Position(node.End()).Offset
	return string(src[start:end])
}
