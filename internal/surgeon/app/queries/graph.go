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
// When summary is true, each package includes a one-line description from its package doc comment.
// When deps is true, each package includes its internal import dependencies (filtered to the project module).
// When symbols is true and recursive is false, only the target directory is inspected (no sub-packages).
// When symbols is false, all packages under dir are always returned regardless of recursive.
// When tests is true, _test.go files are included; unexported symbols in test files are always shown.
func (h *SurgeonQueriesHandler) Graph(ctx context.Context, dir string, symbols, summary, deps, recursive, tests bool) ([]domain.GraphPackage, error) {
	packageFiles := make(map[string][]string)

	isTestFile := func(name string) bool {
		return strings.HasSuffix(name, "_test.go")
	}

	if symbols && !recursive {
		// Non-recursive: only .go files directly in dir.
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".go") {
				continue
			}
			if isTestFile(name) && !tests {
				continue
			}
			packageFiles[dir] = append(packageFiles[dir], filepath.Join(dir, name))
		}
	} else {
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
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			if isTestFile(info.Name()) && !tests {
				return nil
			}

			dirPath := filepath.Dir(path)
			packageFiles[dirPath] = append(packageFiles[dirPath], path)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	var modulePath string
	if deps {
		modulePath, _ = findModulePath(dir)
	}

	var pkgPaths []string
	for p := range packageFiles {
		pkgPaths = append(pkgPaths, p)
	}
	sort.Strings(pkgPaths)

	var packages []domain.GraphPackage
	for _, pkgPath := range pkgPaths {
		pkg := domain.GraphPackage{Path: pkgPath}

		files := packageFiles[pkgPath]
		sort.Strings(files)

		if summary {
			pkg.Summary = h.extractPackageSummary(ctx, files)
		}

		if deps {
			pkg.Deps = h.extractPackageDeps(ctx, files, modulePath)
		}

		if symbols {
			for _, filePath := range files {
				includeUnexported := tests && isTestFile(filepath.Base(filePath))
				gf, err := h.extractGraphSymbols(ctx, filePath, includeUnexported)
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

func (h *SurgeonQueriesHandler) extractGraphSymbols(ctx context.Context, path string, includeUnexported bool) (domain.GraphFile, error) {
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
			if !d.Name.IsExported() && !includeUnexported {
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
				if !ok {
					continue
				}
				if !ts.Name.IsExported() && !includeUnexported {
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

// extractPackageSummary parses the package doc comment from the best candidate file
// (doc.go if present, otherwise first non-test file alphabetically) and returns the first line
// with the "Package <name>" prefix stripped.
func (h *SurgeonQueriesHandler) extractPackageSummary(ctx context.Context, files []string) string {
	if len(files) == 0 {
		return ""
	}

	// files are already sorted; prefer doc.go, skip test files.
	target := ""
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		if filepath.Base(f) == "doc.go" {
			target = f
			break
		}
		if target == "" {
			target = f
		}
	}
	if target == "" {
		return "" // only test files in package
	}

	src, err := h.fs.ReadFile(ctx, target)
	if err != nil {
		return ""
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, target, src, parser.PackageClauseOnly|parser.ParseComments)
	if err != nil {
		return ""
	}

	if f.Doc == nil {
		return ""
	}

	// f.Doc.Text() returns comment text without markers, trimmed of leading/trailing whitespace per line.
	text := strings.TrimSpace(f.Doc.Text())
	// Take only the first line.
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	text = strings.TrimSpace(text)

	// Strip the conventional "Package <name>" prefix.
	lower := strings.ToLower(text)
	if strings.HasPrefix(lower, "package ") {
		rest := text[len("package "):]
		idx := strings.IndexByte(rest, ' ')
		if idx >= 0 {
			return strings.TrimSpace(rest[idx+1:])
		}
		return ""
	}

	return text
}

// extractPackageDeps parses import statements from all non-test files in the package and returns
// the subset that belong to the project module, shortened to paths relative to the module root.
func (h *SurgeonQueriesHandler) extractPackageDeps(ctx context.Context, files []string, modulePath string) []string {
	if modulePath == "" {
		return nil
	}

	seen := make(map[string]struct{})

	for _, filePath := range files {
		if strings.HasSuffix(filePath, "_test.go") {
			continue
		}

		src, err := h.fs.ReadFile(ctx, filePath)
		if err != nil {
			continue
		}

		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filePath, src, parser.ImportsOnly)
		if err != nil {
			continue
		}

		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			prefix := modulePath + "/"
			if strings.HasPrefix(importPath, prefix) {
				seen[importPath[len(prefix):]] = struct{}{}
			}
		}
	}

	if len(seen) == 0 {
		return nil
	}

	deps := make([]string, 0, len(seen))
	for dep := range seen {
		deps = append(deps, dep)
	}
	sort.Strings(deps)
	return deps
}

// findModulePath walks up from startDir looking for go.mod and returns the declared module path.
func findModulePath(startDir string) (string, error) {
	dir := startDir
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "module ") {
					return strings.TrimSpace(line[len("module "):]), nil
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found")
		}
		dir = parent
	}
}
