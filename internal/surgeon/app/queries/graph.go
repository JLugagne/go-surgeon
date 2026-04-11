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
//
// Filtering options in GraphOptions:
//   - Depth: limits how many directory levels deep the walk goes (0 = unlimited).
//   - Exclude: glob patterns matched against directory names to skip during walk.
//   - Focus: when set, only the focused package gets full detail (symbols, summary, deps);
//     all other packages appear as path-only entries.
//   - TokenBudget: when set, output is progressively truncated to fit within the approximate
//     token count (see truncateToTokenBudget).
func (h *SurgeonQueriesHandler) Graph(ctx context.Context, opts domain.GraphOptions) ([]domain.GraphPackage, error) {
	var resolvedModule *ModuleInfo
	if opts.Module != "" {
		info, err := h.resolver.Resolve(ctx, opts.Module)
		if err != nil {
			return nil, err
		}
		resolvedModule = info
		if opts.Dir != "" && opts.Dir != "." {
			if filepath.IsAbs(opts.Dir) {
				return nil, fmt.Errorf("--dir must be a relative path when --module is set; got %q", opts.Dir)
			}
			opts.Dir = filepath.Join(info.Dir, opts.Dir)
		} else {
			opts.Dir = info.Dir
		}
		if opts.Focus != "" && !filepath.IsAbs(opts.Focus) {
			opts.Focus = filepath.Join(info.Dir, opts.Focus)
		}
	}

	dir := opts.Dir
	symbols := opts.Symbols
	summary := opts.Summary
	deps := opts.Deps
	recursive := opts.Recursive
	tests := opts.Tests

	packageFiles := make(map[string][]string)

	isTestFile := func(name string) bool {
		return strings.HasSuffix(name, "_test.go")
	}

	// matchesExclude checks if a directory name matches any exclude glob pattern.
	matchesExclude := func(name string) bool {
		for _, pattern := range opts.Exclude {
			if matched, _ := filepath.Match(pattern, name); matched {
				return true
			}
		}
		return false
	}

	// relativeDepth returns the depth of path relative to baseDir.
	// e.g. baseDir=".", path="internal/foo" => depth 2
	relativeDepth := func(baseDir, path string) int {
		rel, err := filepath.Rel(baseDir, path)
		if err != nil || rel == "." {
			return 0
		}
		return strings.Count(rel, string(filepath.Separator)) + 1
	}

	if symbols && !recursive {
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
				return err
			}
			if info.IsDir() {
				name := info.Name()
				if name == "vendor" || (strings.HasPrefix(name, ".") && path != dir) {
					return filepath.SkipDir
				}
				if matchesExclude(name) {
					return filepath.SkipDir
				}
				if opts.Depth > 0 && relativeDepth(dir, path) > opts.Depth {
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

	// When --focus is set, determine which packages get full detail.
	isFocused := func(pkgPath string) bool {
		if opts.Focus == "" {
			return true // no focus means all packages get full detail
		}
		return pkgPath == opts.Focus || strings.HasPrefix(pkgPath, opts.Focus+"/")
	}

	var packages []domain.GraphPackage
	for _, pkgPath := range pkgPaths {
		pkg := domain.GraphPackage{Path: pkgPath}

		files := packageFiles[pkgPath]
		sort.Strings(files)

		focused := isFocused(pkgPath)

		if summary && focused {
			pkg.Summary = h.extractPackageSummary(ctx, files)
		}

		if deps && focused {
			pkg.Deps = h.extractPackageDeps(ctx, files, modulePath)
		}

		if symbols && focused {
			for _, filePath := range files {
				includeUnexported := tests && isTestFile(filepath.Base(filePath))
				gf, err := h.extractGraphSymbols(ctx, filePath, includeUnexported)
				if err != nil {
					pkg.Files = append(pkg.Files, domain.GraphFile{
						Path:    filePath,
						Symbols: []string{fmt.Sprintf("WARNING: failed to parse: %v", err)},
					})
					continue
				}
				if len(gf.Symbols) > 0 {
					pkg.Files = append(pkg.Files, gf)
				}
			}
		}

		packages = append(packages, pkg)
	}

	if opts.TokenBudget > 0 {
		packages = truncateToTokenBudget(packages, opts.TokenBudget)
	}

	if resolvedModule != nil {
		moduleRoot := resolvedModule.Dir
		for i := range packages {
			if rel, err := filepath.Rel(moduleRoot, packages[i].Path); err == nil {
				packages[i].Path = rel
			}
			for j := range packages[i].Files {
				if rel, err := filepath.Rel(moduleRoot, packages[i].Files[j].Path); err == nil {
					packages[i].Files[j].Path = rel
				}
			}
		}
		header := domain.GraphPackage{
			Path: fmt.Sprintf("# Module: %s @ %s\n# Location: %s", resolvedModule.Path, resolvedModule.Version, resolvedModule.Dir),
		}
		packages = append([]domain.GraphPackage{header}, packages...)
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

// estimateTokens approximates the token count of the graph output.
// Uses the rough heuristic of 1 token ≈ 4 characters for code/English.
func estimateTokens(packages []domain.GraphPackage) int {
	total := 0
	for _, pkg := range packages {
		total += len(pkg.Path)
		total += len(pkg.Summary)
		for _, dep := range pkg.Deps {
			total += len(dep) + 2 // separator
		}
		for _, file := range pkg.Files {
			total += len(file.Path)
			for _, sym := range file.Symbols {
				total += len(sym)
			}
		}
	}
	return total / 4
}

// truncateToTokenBudget progressively reduces graph detail to fit within
// the given token budget. Truncation levels, applied in order:
//  1. Strip doc summaries
//  2. Strip dependency lists
//  3. Strip symbol signatures (keep file paths)
//  4. Strip file lists (keep package paths only)
//  5. Truncate package list from the end
func truncateToTokenBudget(packages []domain.GraphPackage, budget int) []domain.GraphPackage {
	if estimateTokens(packages) <= budget {
		return packages
	}

	// Level 1: strip summaries.
	for i := range packages {
		packages[i].Summary = ""
	}
	if estimateTokens(packages) <= budget {
		return packages
	}

	// Level 2: strip deps.
	for i := range packages {
		packages[i].Deps = nil
	}
	if estimateTokens(packages) <= budget {
		return packages
	}

	// Level 3: strip symbols, keep file paths.
	for i := range packages {
		for j := range packages[i].Files {
			packages[i].Files[j].Symbols = nil
		}
	}
	if estimateTokens(packages) <= budget {
		return packages
	}

	// Level 4: strip files entirely.
	for i := range packages {
		packages[i].Files = nil
	}
	if estimateTokens(packages) <= budget {
		return packages
	}

	// Level 5: truncate package list.
	for len(packages) > 1 && estimateTokens(packages) > budget {
		packages = packages[:len(packages)-1]
	}
	return packages
}
