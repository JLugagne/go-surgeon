package queries

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// FindDefinition returns only the declaration site of a symbol. It's a
// thin wrapper over the references machinery that stops after the
// object resolves — we still load packages with NeedTypes because the
// symbol might be a method whose receiver must be matched by type.
func (h *SurgeonQueriesHandler) FindDefinition(ctx context.Context, query domain.ReferencesQuery) (domain.ReferencesResult, error) {
	obj, pkg, loaded, err := h.resolveSymbol(ctx, query)
	if err != nil {
		return domain.ReferencesResult{}, err
	}
	def := objectLocation(obj, loaded)
	return domain.ReferencesResult{
		Symbol:     symbolRefFromObject(obj, pkg),
		Kind:       classifyObject(obj),
		Definition: def,
	}, nil
}

// FindReferences resolves the symbol and then walks every file in every
// loaded package, consulting types.Info.Uses / types.Info.Defs to
// attribute every matching identifier back to the same types.Object.
// The resulting slice includes no duplicates and is sorted by
// file/line/column.
func (h *SurgeonQueriesHandler) FindReferences(ctx context.Context, query domain.ReferencesQuery) (domain.ReferencesResult, error) {
	obj, pkg, loaded, err := h.resolveSymbol(ctx, query)
	if err != nil {
		return domain.ReferencesResult{}, err
	}

	def := objectLocation(obj, loaded)
	refs := collectReferences(obj, loaded)
	sortLocations(refs)

	out := domain.ReferencesResult{
		Symbol:     symbolRefFromObject(obj, pkg),
		Kind:       classifyObject(obj),
		References: refs,
	}
	if query.IncludeDefinition {
		out.Definition = def
	}
	return out, nil
}

// loadedPackages bundles the output of packages.Load with a shared
// token.FileSet so callers can translate token.Pos values back to
// file:line:column without re-parsing.
type loadedPackages struct {
	fset *token.FileSet
	pkgs []*packages.Package
}

// resolveSymbol loads all packages rooted at query.Dir and then walks
// their type information until it finds the declaration of the named
// symbol. It is the workhorse that both FindDefinition, FindReferences,
// and the rename command rely on.
func (h *SurgeonQueriesHandler) resolveSymbol(ctx context.Context, query domain.ReferencesQuery) (types.Object, *packages.Package, *loadedPackages, error) {
	if query.Symbol.Name == "" {
		return nil, nil, nil, fmt.Errorf("symbol.name is required")
	}

	dir := query.Dir
	if dir == "" {
		dir = "."
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolve dir %q: %w", dir, err)
	}

	loadedPkgs, err := h.loader.Load(ctx, absDir, query.Tests)
	if err != nil {
		return nil, nil, nil, err
	}
	fset := loadedPkgs.Fset
	pkgs := loadedPkgs.Pkgs
	if len(pkgs) == 0 {
		return nil, nil, nil, fmt.Errorf("no Go packages found under %q", absDir)
	}

	// Surface hard loader errors (missing deps, parse failures). A
	// failing type-check in one package is fatal because we rely on
	// Uses/Defs; don't silently return partial results.
	for _, p := range pkgs {
		for _, e := range p.Errors {
			// Only elevate errors from packages the user asked for.
			// Transitive-dep errors are surfaced with a package prefix.
			if e.Kind == packages.TypeError || e.Kind == packages.ParseError {
				return nil, nil, nil, fmt.Errorf("%s: %s", p.PkgPath, e.Msg)
			}
		}
	}

	loaded := &loadedPackages{fset: fset, pkgs: pkgs}

	obj, pkg, err := findObject(loaded, query.Symbol)
	if err != nil {
		return nil, nil, nil, err
	}
	return obj, pkg, loaded, nil
}

// findObject walks the type-checked packages looking for the symbol's
// defining types.Object. We first try an exact match using all the
// disambiguators the caller provided (receiver, package, file/line);
// on failure we fall back to a "single unambiguous result" search and
// report a useful error if the name is ambiguous.
func findObject(loaded *loadedPackages, ref domain.SymbolRef) (types.Object, *packages.Package, error) {
	type candidate struct {
		obj types.Object
		pkg *packages.Package
	}
	var candidates []candidate

	for _, p := range loaded.pkgs {
		if p.Types == nil || p.TypesInfo == nil {
			continue
		}
		if ref.Package != "" && !packageMatches(p, ref.Package) {
			continue
		}

		// Package-level scope first — covers functions, types,
		// package-level vars/consts.
		if obj := p.Types.Scope().Lookup(ref.Name); obj != nil {
			if ref.Receiver == "" && matchesFileLine(loaded.fset, obj.Pos(), ref) {
				candidates = append(candidates, candidate{obj, p})
			}
		}

		// Methods & fields: walk TypesInfo.Defs — this includes every
		// declaration the type-checker saw in the package's syntax.
		for id, obj := range p.TypesInfo.Defs {
			if obj == nil || id.Name != ref.Name {
				continue
			}
			if !matchesFileLine(loaded.fset, obj.Pos(), ref) {
				continue
			}
			// Method receiver filter.
			if ref.Receiver != "" {
				fn, ok := obj.(*types.Func)
				if !ok {
					continue
				}
				sig, ok := fn.Type().(*types.Signature)
				if !ok || sig.Recv() == nil {
					continue
				}
				if receiverTypeName(sig.Recv().Type()) != ref.Receiver {
					continue
				}
			}
			// Skip duplicates (package-scope lookup above may already
			// have added the same object).
			dup := false
			for _, c := range candidates {
				if c.obj == obj {
					dup = true
					break
				}
			}
			if !dup {
				candidates = append(candidates, candidate{obj, p})
			}
		}
	}

	if len(candidates) == 0 {
		hint := ""
		if ref.Receiver != "" {
			hint = fmt.Sprintf(" (receiver=%s)", ref.Receiver)
		}
		return nil, nil, fmt.Errorf("symbol %q%s not found in loaded packages", ref.Name, hint)
	}
	if len(candidates) > 1 {
		var sb strings.Builder
		fmt.Fprintf(&sb, "symbol %q is ambiguous (%d matches); refine with receiver/package/file+line:\n", ref.Name, len(candidates))
		for _, c := range candidates {
			pos := loaded.fset.Position(c.obj.Pos())
			fmt.Fprintf(&sb, "  - %s at %s:%d\n", c.pkg.PkgPath, pos.Filename, pos.Line)
		}
		return nil, nil, fmt.Errorf("%s", sb.String())
	}
	return candidates[0].obj, candidates[0].pkg, nil
}

// packageMatches checks whether the caller's package hint matches a
// loaded package. We accept either the full import path or the bare
// package name so callers don't always have to know the import path.
func packageMatches(p *packages.Package, hint string) bool {
	if p == nil {
		return false
	}
	if p.PkgPath == hint {
		return true
	}
	if p.Name == hint {
		return true
	}
	return false
}

// matchesFileLine returns true when ref's File/Line are either unset
// (caller doesn't care about position) or line up with the given
// declaration position.
func matchesFileLine(fset *token.FileSet, pos token.Pos, ref domain.SymbolRef) bool {
	if ref.File == "" && ref.Line == 0 {
		return true
	}
	p := fset.Position(pos)
	if ref.Line != 0 && ref.Line != p.Line {
		return false
	}
	if ref.File != "" {
		if !samePath(ref.File, p.Filename) {
			return false
		}
	}
	return true
}

// samePath tolerates mismatched relative/absolute paths by comparing
// both forms.
func samePath(a, b string) bool {
	if a == b {
		return true
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return false
	}
	return absA == absB
}

// receiverTypeName extracts the bare type name from a method's
// receiver type, stripping pointer stars and type parameters.
func receiverTypeName(t types.Type) string {
	switch tt := t.(type) {
	case *types.Pointer:
		return receiverTypeName(tt.Elem())
	case *types.Named:
		return tt.Obj().Name()
	case interface{ Obj() *types.TypeName }:
		// Covers *types.Alias and any future variant carrying a TypeName.
		return tt.Obj().Name()
	}
	return ""
}

// collectReferences walks every file's TypesInfo.Uses map and returns
// one Location per identifier whose resolved object matches target.
// It intentionally excludes the declaring identifier (that one lives
// in Defs, not Uses, and we surface it via Definition).
func collectReferences(target types.Object, loaded *loadedPackages) []domain.Location {
	fileSrc := make(map[string][]byte)
	var locations []domain.Location

	for _, p := range loaded.pkgs {
		if p.TypesInfo == nil {
			continue
		}
		for id, obj := range p.TypesInfo.Uses {
			if !sameObject(obj, target) {
				continue
			}
			loc := identLocation(loaded.fset, id, fileSrc)
			if loc.File == "" {
				continue
			}
			locations = append(locations, loc)
		}
	}

	return dedupLocations(locations)
}

// sameObject handles the embedded-field / interface-method-satisfaction
// corner cases go/types models as distinct Objects that nevertheless
// refer to the same declared symbol.
func sameObject(a, b types.Object) bool {
	if a == nil || b == nil {
		return false
	}
	return a == b || a.Pos() == b.Pos() && a.Name() == b.Name() && a.Pkg() == b.Pkg()
}

// identLocation turns an *ast.Ident into a domain.Location, lazily
// loading the enclosing file's source so we can attach a LineText
// preview for CLI/MCP consumption.
func identLocation(fset *token.FileSet, id *ast.Ident, fileSrc map[string][]byte) domain.Location {
	pos := fset.Position(id.Pos())
	if pos.Filename == "" {
		return domain.Location{}
	}
	line := readLine(fileSrc, pos.Filename, pos.Line)
	endPos := fset.Position(id.End())
	return domain.Location{
		File:      pos.Filename,
		Line:      pos.Line,
		Column:    pos.Column,
		Offset:    pos.Offset,
		EndOffset: endPos.Offset,
		LineText:  line,
	}
}

// objectLocation mirrors identLocation but for a types.Object's
// declaration position. The identifier length is recovered from the
// object's Name so we can still report a useful EndOffset for the
// declaration site (handy for rename).
func objectLocation(obj types.Object, loaded *loadedPackages) domain.Location {
	if obj == nil {
		return domain.Location{}
	}
	pos := loaded.fset.Position(obj.Pos())
	if pos.Filename == "" {
		return domain.Location{}
	}
	fileSrc := make(map[string][]byte)
	line := readLine(fileSrc, pos.Filename, pos.Line)
	return domain.Location{
		File:      pos.Filename,
		Line:      pos.Line,
		Column:    pos.Column,
		Offset:    pos.Offset,
		EndOffset: pos.Offset + len(obj.Name()),
		LineText:  line,
	}
}

// readLine returns the raw text of the given 1-based line in filename,
// caching file contents in fileSrc so we don't re-read them per match.
func readLine(fileSrc map[string][]byte, filename string, line int) string {
	src, ok := fileSrc[filename]
	if !ok {
		src, _ = os.ReadFile(filename)
		fileSrc[filename] = src
	}
	if len(src) == 0 {
		return ""
	}
	cur := 1
	start := 0
	for i, b := range src {
		if cur == line {
			end := i + 1
			// Find line end.
			for j := i; j < len(src); j++ {
				if src[j] == '\n' {
					end = j
					break
				}
				end = j + 1
			}
			return strings.TrimRight(string(src[start:end]), "\r\n")
		}
		if b == '\n' {
			cur++
			start = i + 1
		}
	}
	return ""
}

// dedupLocations removes exact duplicates (same file + offset) from
// the reference list. Go's type checker can attribute the same
// identifier more than once when a file is loaded as part of multiple
// packages (common with Tests=true).
func dedupLocations(locs []domain.Location) []domain.Location {
	seen := make(map[string]struct{}, len(locs))
	out := make([]domain.Location, 0, len(locs))
	for _, l := range locs {
		key := fmt.Sprintf("%s:%d:%d", l.File, l.Offset, l.EndOffset)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, l)
	}
	return out
}

// sortLocations sorts by file, then line, then column.
func sortLocations(locs []domain.Location) {
	sort.Slice(locs, func(i, j int) bool {
		if locs[i].File != locs[j].File {
			return locs[i].File < locs[j].File
		}
		if locs[i].Line != locs[j].Line {
			return locs[i].Line < locs[j].Line
		}
		return locs[i].Column < locs[j].Column
	})
}

// symbolRefFromObject projects a resolved types.Object back into the
// domain SymbolRef so callers see the canonical receiver/package the
// loader resolved (even if their input was terse).
func symbolRefFromObject(obj types.Object, pkg *packages.Package) domain.SymbolRef {
	ref := domain.SymbolRef{Name: obj.Name()}
	if pkg != nil {
		ref.Package = pkg.PkgPath
	}
	if fn, ok := obj.(*types.Func); ok {
		if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
			ref.Receiver = receiverTypeName(sig.Recv().Type())
		}
	}
	return ref
}

// classifyObject returns a short string tag matching the constants
// used in the OutlineEntry kind field.
func classifyObject(obj types.Object) string {
	switch o := obj.(type) {
	case *types.Func:
		if sig, ok := o.Type().(*types.Signature); ok && sig.Recv() != nil {
			return "method"
		}
		return "func"
	case *types.TypeName:
		return "type"
	case *types.Var:
		if o.IsField() {
			return "field"
		}
		return "var"
	case *types.Const:
		return "const"
	case *types.PkgName:
		return "package"
	}
	return "symbol"
}
