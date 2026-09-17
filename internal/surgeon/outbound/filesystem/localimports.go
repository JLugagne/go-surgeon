package filesystem

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/tools/go/ast/astutil"
)

// moduleIndex maps the package names of a Go module to their import
// paths, so import resolution can prefer the current module (including
// its internal/ tree) over same-named third-party packages (issue #34).
type moduleIndex struct {
	modPath string
	byName  map[string]string          // unique package name -> import path
	exports map[string]map[string]bool // import path -> exported decl names
}

var (
	moduleIndexMu    sync.Mutex
	moduleIndexCache = map[string]*moduleIndex{}
)

// findModuleRoot walks up from the file's directory until it finds a
// go.mod and returns that directory together with the module path.
func findModuleRoot(filePath string) (root, modPath string) {
	dir := filepath.Dir(filePath)
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			if mp := parseModulePath(data); mp != "" {
				return dir, mp
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ""
		}
		dir = parent
	}
}

func parseModulePath(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "module ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			return strings.Trim(fields[1], `"`)
		}
	}
	return ""
}

// localModuleIndex returns the cached package index for the module that
// contains root, building it on first use.
func localModuleIndex(root string) *moduleIndex {
	moduleIndexMu.Lock()
	if idx, ok := moduleIndexCache[root]; ok {
		moduleIndexMu.Unlock()
		return idx
	}
	moduleIndexMu.Unlock()

	idx := buildModuleIndex(root)

	moduleIndexMu.Lock()
	moduleIndexCache[root] = idx
	moduleIndexMu.Unlock()
	return idx
}

// invalidateModuleIndex drops cached package indexes so a newly created
// package becomes resolvable on the next write.
func invalidateModuleIndex() {
	moduleIndexMu.Lock()
	moduleIndexCache = map[string]*moduleIndex{}
	moduleIndexMu.Unlock()
}

type dirPackage struct {
	name    string
	exports map[string]bool
	valid   bool
}

func buildModuleIndex(root string) *moduleIndex {
	idx := &moduleIndex{byName: map[string]string{}, exports: map[string]map[string]bool{}}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return idx
	}
	idx.modPath = parseModulePath(data)
	if idx.modPath == "" {
		return idx
	}

	dirs := map[string]*dirPackage{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if p == root {
				return nil
			}
			name := d.Name()
			if name == "vendor" || name == "testdata" || name == "node_modules" ||
				strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		dir := filepath.Dir(p)
		info := dirs[dir]
		if info == nil {
			info = &dirPackage{exports: map[string]bool{}, valid: true}
			dirs[dir] = info
		}
		pkgName, exports, perr := packageSymbols(p)
		if perr != nil {
			return nil
		}
		if pkgName == "" {
			return nil
		}
		if info.name == "" {
			info.name = pkgName
		} else if info.name != pkgName {
			info.valid = false
		}
		for name := range exports {
			info.exports[name] = true
		}
		return nil
	})

	// Detect duplicate package names across directories: an ambiguous name
	// is drop()ed so we never guess the wrong local package.
	counts := map[string]int{}
	for _, info := range dirs {
		if info.name == "" || info.name == "main" || !info.valid {
			continue
		}
		counts[info.name]++
	}
	for dir, info := range dirs {
		if info.name == "" || info.name == "main" || !info.valid || counts[info.name] != 1 {
			continue
		}
		rel, rerr := filepath.Rel(root, dir)
		if rerr != nil {
			continue
		}
		importPath := idx.modPath
		if rel != "." {
			importPath = idx.modPath + "/" + filepath.ToSlash(rel)
		}
		idx.byName[info.name] = importPath
		idx.exports[importPath] = info.exports
	}
	return idx
}

// packageSymbols parses one file and returns its package name plus the
// set of exported top-level declaration names.
func packageSymbols(path string) (string, map[string]bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return "", nil, err
	}
	exports := map[string]bool{}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil && d.Name != nil && ast.IsExported(d.Name.Name) {
				exports[d.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name != nil && ast.IsExported(s.Name.Name) {
						exports[s.Name.Name] = true
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n != nil && ast.IsExported(n.Name) {
							exports[n.Name] = true
						}
					}
				}
			}
		}
	}
	return f.Name.Name, exports, nil
}

// collectUnresolvedQualifiers returns package-qualified identifiers
// (e.g. store in store.Get) that have no matching import and are not
// locally declared names.
func collectUnresolvedQualifiers(f *ast.File) map[string]bool {
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
	return unresolved
}

// selectorNames returns the set of selector identifiers used on qualifier.
func selectorNames(f *ast.File, qualifier string) map[string]bool {
	names := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == qualifier && sel.Sel != nil {
			names[sel.Sel.Name] = true
		}
		return true
	})
	return names
}

// resolveLocalImports adds imports for qualifiers that name a package of
// the current module. It only adds an import when the local package
// exports at least one of the referenced selectors, so a same-named
// variable or type never triggers a bogus import.
func resolveLocalImports(path string, content []byte) ([]byte, []string) {
	root, _ := findModuleRoot(path)
	if root == "" {
		return content, nil
	}
	idx := localModuleIndex(root)
	if len(idx.byName) == 0 {
		return content, nil
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, content, parser.ParseComments)
	if err != nil {
		return content, nil
	}
	unresolved := collectUnresolvedQualifiers(f)
	if len(unresolved) == 0 {
		return content, nil
	}

	var toAdd []string
	for name := range unresolved {
		importPath, ok := idx.byName[name]
		if !ok {
			continue
		}
		exports := idx.exports[importPath]
		matched := false
		for sel := range selectorNames(f, name) {
			if exports[sel] {
				matched = true
				break
			}
		}
		if matched {
			toAdd = append(toAdd, importPath)
		}
	}
	if len(toAdd) == 0 {
		return content, nil
	}

	sort.Strings(toAdd)
	for _, imp := range toAdd {
		astutil.AddImport(fset, f, imp)
	}
	var buf bytes.Buffer
	if ferr := format.Node(&buf, fset, f); ferr != nil {
		return content, nil
	}
	return buf.Bytes(), toAdd
}
