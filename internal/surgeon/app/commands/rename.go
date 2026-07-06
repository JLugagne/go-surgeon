package commands

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/tools/go/packages"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// Rename renames the symbol identified by req.Symbol to req.NewName
// across every package the loader finds under req.Dir. The rewrite is
// position-based: we walk types.Info.Uses/Defs, collect byte offsets
// of each identifier tied to the resolved object, then splice the new
// name into the file's bytes at those offsets.
//
// Why position-based rather than source-transform: offsets come
// straight from the type-checker and therefore cover exactly the
// sites that resolve to the renamed object, including embedded-field
// uses and interface-method-satisfaction points. A textual replace
// would false-match unrelated identifiers that happen to share the
// name; a full AST rewrite+print would lose layout (spacing,
// comments). Offsets preserve layout and still respect type identity.
func (h *ExecutePlanHandler) Rename(ctx context.Context, req domain.RenameRequest) (domain.RenameResult, error) {
	if req.Symbol.Name == "" {
		return domain.RenameResult{}, &domain.Error{
			Code:    "INVALID_ARGUMENT",
			Message: "rename: symbol.name is required",
		}
	}
	if req.NewName == "" {
		return domain.RenameResult{}, &domain.Error{
			Code:    "INVALID_ARGUMENT",
			Message: "rename: new_name is required",
		}
	}
	if req.NewName == req.Symbol.Name {
		return domain.RenameResult{}, &domain.Error{
			Code:    "INVALID_ARGUMENT",
			Message: fmt.Sprintf("rename: new name %q equals old name", req.NewName),
		}
	}
	if !isValidGoIdent(req.NewName) {
		return domain.RenameResult{}, &domain.Error{
			Code:    "INVALID_ARGUMENT",
			Message: fmt.Sprintf("rename: %q is not a valid Go identifier", req.NewName),
		}
	}
	// Preserve exported-ness by default: renaming an exported symbol
	// to an unexported one (or vice versa) is almost always a mistake,
	// so we reject it unless the caller opts in via AllowExportChange.
	// When opt-in is set, we still emit a warning on the result so the
	// caller can notice the flip.
	var exportChangeWarning string
	if isExportedIdent(req.Symbol.Name) != isExportedIdent(req.NewName) {
		if !req.AllowExportChange {
			return domain.RenameResult{}, &domain.Error{
				Code:    "INVALID_ARGUMENT",
				Message: fmt.Sprintf("rename: changing export status (%q → %q) is not allowed; rename in two steps if intentional, or pass allow_export_change=true", req.Symbol.Name, req.NewName),
			}
		}
		oldStatus := "unexported"
		if isExportedIdent(req.Symbol.Name) {
			oldStatus = "exported"
		}
		newStatus := "unexported"
		if isExportedIdent(req.NewName) {
			newStatus = "exported"
		}
		exportChangeWarning = fmt.Sprintf("export status changed: %q (%s) → %q (%s)", req.Symbol.Name, oldStatus, req.NewName, newStatus)
	}

	dir := req.Dir
	if dir == "" {
		dir = "."
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return domain.RenameResult{}, &domain.Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("resolve dir: %v", err)}
	}

	loaded, err := h.loader.Load(ctx, absDir, req.Tests)
	if err != nil {
		return domain.RenameResult{}, &domain.Error{Code: "LOAD_ERROR", Message: fmt.Sprintf("load packages: %v", err)}
	}
	fset := loaded.Fset
	pkgs := loaded.Pkgs
	if len(pkgs) == 0 {
		return domain.RenameResult{}, &domain.Error{Code: "NOT_FOUND", Message: fmt.Sprintf("no Go packages under %q", absDir)}
	}
	for _, p := range pkgs {
		for _, e := range p.Errors {
			if e.Kind == packages.TypeError || e.Kind == packages.ParseError {
				return domain.RenameResult{}, &domain.Error{
					Code:    "LOAD_ERROR",
					Message: fmt.Sprintf("%s: %s", p.PkgPath, e.Msg),
				}
			}
		}
	}

	target, kind, err := locateSymbol(fset, pkgs, req.Symbol)
	if err != nil {
		return domain.RenameResult{}, &domain.Error{Code: "NOT_FOUND", Message: err.Error()}
	}

	// Pre-flight: refuse to clobber an existing identifier in the
	// same scope. We only check the target object's immediate parent
	// scope; deeper scopes will still compile if they shadow, and the
	// user's build_check will catch anything we miss.
	if err := checkNoCollision(target, req.NewName); err != nil {
		return domain.RenameResult{}, &domain.Error{Code: "CONFLICT", Message: err.Error()}
	}

	// Pre-flight: refuse a rename that would silently rebind an existing
	// reference by shadowing across nested scopes. Such a rename compiles
	// cleanly, so build_check cannot catch it — we must reject it here.
	if err := checkNoShadowCapture(fset, pkgs, target, req.NewName); err != nil {
		return domain.RenameResult{}, &domain.Error{Code: "CONFLICT", Message: err.Error()}
	}

	locs := collectRenameSites(fset, pkgs, target, req.Symbol.Name)
	if len(locs) == 0 {
		return domain.RenameResult{}, &domain.Error{
			Code:    "NOT_FOUND",
			Message: fmt.Sprintf("rename: no occurrences of %q found", req.Symbol.Name),
		}
	}

	// Group by file, sort each group by Offset descending so splicing
	// doesn't invalidate later offsets in the same file.
	byFile := map[string][]domain.Location{}
	for _, l := range locs {
		byFile[l.File] = append(byFile[l.File], l)
	}
	var files []string
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)

	result := domain.RenameResult{
		OldName: req.Symbol.Name,
		NewName: req.NewName,
		Kind:    kind,
		DryRun:  req.DryRun,
	}
	if exportChangeWarning != "" {
		result.Warnings = append(result.Warnings, exportChangeWarning)
	}

	// Buffer every rewritten file and validate all per-site guards before
	// writing anything. A guard failure on a later file must not leave an
	// earlier file half-rewritten on disk, so writes are deferred until
	// the whole batch has passed validation (all-or-nothing at the
	// validation level).
	type pendingWrite struct {
		file    string
		content []byte
	}
	pending := make([]pendingWrite, 0, len(files))
	for _, file := range files {
		group := byFile[file]
		sort.Slice(group, func(i, j int) bool { return group[i].Offset > group[j].Offset })

		src, readErr := h.fs.ReadFile(ctx, file)
		if readErr != nil {
			return domain.RenameResult{}, &domain.Error{Code: "READ_ERROR", Message: fmt.Sprintf("read %s: %v", file, readErr), Err: readErr}
		}
		working := make([]byte, len(src))
		copy(working, src)
		for _, l := range group {
			if l.Offset < 0 || l.EndOffset > len(working) || l.Offset > l.EndOffset {
				return domain.RenameResult{}, &domain.Error{
					Code:    "INTERNAL",
					Message: fmt.Sprintf("rename: offset %d..%d out of range for %s (len=%d)", l.Offset, l.EndOffset, file, len(working)),
				}
			}
			if string(working[l.Offset:l.EndOffset]) != req.Symbol.Name {
				return domain.RenameResult{}, &domain.Error{
					Code:    "INTERNAL",
					Message: fmt.Sprintf("rename: unexpected text %q at %s:%d (expected %q)", string(working[l.Offset:l.EndOffset]), file, l.Line, req.Symbol.Name),
				}
			}
			working = append(working[:l.Offset], append([]byte(req.NewName), working[l.EndOffset:]...)...)
		}
		pending = append(pending, pendingWrite{file: file, content: working})
		result.FilesModified = append(result.FilesModified, file)
	}

	if !req.DryRun {
		for _, pw := range pending {
			if _, writeErr := h.fs.WriteFile(ctx, pw.file, pw.content); writeErr != nil {
				return domain.RenameResult{}, &domain.Error{Code: "WRITE_ERROR", Message: fmt.Sprintf("write %s: %v", pw.file, writeErr), Err: writeErr}
			}
		}
	}

	// Sort for a deterministic report.
	sort.Slice(locs, func(i, j int) bool {
		if locs[i].File != locs[j].File {
			return locs[i].File < locs[j].File
		}
		if locs[i].Line != locs[j].Line {
			return locs[i].Line < locs[j].Line
		}
		return locs[i].Column < locs[j].Column
	})
	result.Locations = locs
	return result, nil
}

// locateSymbol resolves a SymbolRef to a types.Object within the
// loaded packages. It mirrors (but intentionally duplicates) the
// query-side resolver so the commands package doesn't depend on the
// queries package.
func locateSymbol(fset *token.FileSet, pkgs []*packages.Package, ref domain.SymbolRef) (types.Object, string, error) {
	type cand struct {
		obj types.Object
		pkg *packages.Package
	}
	var candidates []cand
	// Dedup by declaration position + name, not object identity: with
	// Tests=true the loader returns the same package twice (pkg and
	// pkg [pkg.test]) and each universe carries its own types.Object
	// for the same declaration. Pointer comparison would report every
	// symbol as "ambiguous (2 matches)".
	seen := make(map[string]struct{})
	addCandidate := func(obj types.Object, p *packages.Package) {
		key := renameObjectPosKey(fset, obj)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		candidates = append(candidates, cand{obj, p})
	}
	for _, p := range pkgs {
		if p.Types == nil || p.TypesInfo == nil {
			continue
		}
		if ref.Package != "" && p.PkgPath != ref.Package && p.Name != ref.Package {
			continue
		}
		if obj := p.Types.Scope().Lookup(ref.Name); obj != nil {
			if ref.Receiver == "" && renameMatchesFileLine(fset, obj.Pos(), ref) {
				addCandidate(obj, p)
			}
		}
		for id, obj := range p.TypesInfo.Defs {
			if obj == nil || id.Name != ref.Name {
				continue
			}
			if !renameMatchesFileLine(fset, obj.Pos(), ref) {
				continue
			}
			if ref.Receiver != "" {
				fn, ok := obj.(*types.Func)
				if !ok {
					continue
				}
				sig, ok := fn.Type().(*types.Signature)
				if !ok || sig.Recv() == nil {
					continue
				}
				if renameReceiverName(sig.Recv().Type()) != ref.Receiver {
					continue
				}
			}
			addCandidate(obj, p)
		}
	}

	if len(candidates) == 0 {
		return nil, "", fmt.Errorf("symbol %q not found", ref.Name)
	}
	if len(candidates) > 1 {
		var sb strings.Builder
		fmt.Fprintf(&sb, "symbol %q is ambiguous (%d matches); refine with receiver/package/file+line:\n", ref.Name, len(candidates))
		for _, c := range candidates {
			pos := fset.Position(c.obj.Pos())
			fmt.Fprintf(&sb, "  - %s at %s:%d\n", c.pkg.PkgPath, pos.Filename, pos.Line)
		}
		return nil, "", fmt.Errorf("%s", sb.String())
	}
	return candidates[0].obj, renameObjectKind(candidates[0].obj), nil
}

// renameMatchesFileLine checks whether the caller's file+line
// disambiguators align with the declaration position.
func renameMatchesFileLine(fset *token.FileSet, pos token.Pos, ref domain.SymbolRef) bool {
	if ref.File == "" && ref.Line == 0 {
		return true
	}
	p := fset.Position(pos)
	if ref.Line != 0 && ref.Line != p.Line {
		return false
	}
	if ref.File != "" {
		absA, errA := filepath.Abs(ref.File)
		absB, errB := filepath.Abs(p.Filename)
		if errA != nil || errB != nil || absA != absB {
			return false
		}
	}
	return true
}

// renameReceiverName strips pointer stars and returns the bare type
// name so the caller can match "BookHandler" against "*BookHandler".
func renameReceiverName(t types.Type) string {
	switch tt := t.(type) {
	case *types.Pointer:
		return renameReceiverName(tt.Elem())
	case *types.Named:
		return tt.Obj().Name()
	case interface{ Obj() *types.TypeName }:
		return tt.Obj().Name()
	}
	return ""
}

// renameObjectKind classifies the resolved object so the caller gets a
// short human-readable tag back.
func renameObjectKind(obj types.Object) string {
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
	}
	return "symbol"
}

// collectRenameSites returns every identifier position that should be
// rewritten: the defining identifier plus each use. We also include
// SelectorExpr.Sel occurrences to cover qualified accesses (pkg.Name,
// Receiver.Method) — those are already in TypesInfo.Uses but the
// AST-walk is a belt-and-braces pass for packages that didn't fully
// type-check.
func collectRenameSites(fset *token.FileSet, pkgs []*packages.Package, target types.Object, oldName string) []domain.Location {
	seen := make(map[string]struct{})
	var out []domain.Location

	record := func(id *ast.Ident) {
		if id == nil || id.Name != oldName {
			return
		}
		pos := fset.Position(id.Pos())
		end := fset.Position(id.End())
		if pos.Filename == "" {
			return
		}
		key := fmt.Sprintf("%s:%d", pos.Filename, pos.Offset)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, domain.Location{
			File:      pos.Filename,
			Line:      pos.Line,
			Column:    pos.Column,
			Offset:    pos.Offset,
			EndOffset: end.Offset,
		})
	}

	for _, p := range pkgs {
		if p.TypesInfo == nil {
			continue
		}
		for id, obj := range p.TypesInfo.Defs {
			if renameSameObject(fset, obj, target) {
				record(id)
			}
		}
		for id, obj := range p.TypesInfo.Uses {
			if renameSameObject(fset, obj, target) {
				record(id)
			}
		}
	}

	return out
}

// renameSameObject matches objects by declaration position + name in
// the shared fset. Pointer or Pkg() identity would drop matches when
// Tests=true loads the same package in two universes (pkg vs
// pkg [pkg.test]) whose *types.Package pointers differ — silently
// skipping every reference living in the test universe.
func renameSameObject(fset *token.FileSet, obj, target types.Object) bool {
	if obj == nil || target == nil {
		return false
	}
	if obj == target {
		return true
	}
	if obj.Name() != target.Name() || !obj.Pos().IsValid() || !target.Pos().IsValid() {
		return false
	}
	po := fset.Position(obj.Pos())
	pt := fset.Position(target.Pos())
	return po.Filename != "" && po.Filename == pt.Filename && po.Offset == pt.Offset
}

// renameObjectPosKey builds a dedup key from an object's declaration
// position and name — stable across the duplicate package universes
// Tests=true produces, unlike the *types.Object pointer.
func renameObjectPosKey(fset *token.FileSet, obj types.Object) string {
	pos := fset.Position(obj.Pos())
	return fmt.Sprintf("%s:%d:%s", pos.Filename, pos.Offset, obj.Name())
}

// checkNoCollision rejects a rename that would collide with an
// already-declared name in the same lexical scope. We limit the check
// to the enclosing scope (package scope for top-level symbols, struct
// scope for fields, etc.) because deeper scopes can legitimately
// shadow the new name without breaking the build.
func checkNoCollision(target types.Object, newName string) error {
	parent := target.Parent()
	if parent == nil {
		// Methods, fields, and interface members have no Parent()
		// scope — their "scope" is the enclosing type, and a
		// collision there will surface as a build error. Skip.
		return nil
	}
	existing := parent.Lookup(newName)
	if existing == nil || existing == target {
		return nil
	}
	// Methods live in their receiver's method set, not in the package
	// scope. A method name does not conflict with a package-level
	// type/func/var/const declaration.
	if fn, ok := existing.(*types.Func); ok {
		if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
			return nil
		}
	}
	return fmt.Errorf("cannot rename %q → %q: name %q already declared in the same scope", target.Name(), newName, newName)
}

// checkNoShadowCapture rejects a rename that would silently rebind an
// existing identifier by introducing (or being shadowed by) a nested
// declaration of newName. checkNoCollision only inspects the target's
// immediate parent scope, so it misses two capture directions that both
// compile cleanly and therefore evade build_check:
//
//	inbound  — a reference to the target lives inside a nested scope that
//	           already declares newName; after the rename that reference
//	           binds to the nested declaration instead of the target.
//	outbound — the target is declared in a nested scope and newName is
//	           declared in an enclosing scope; after the rename the
//	           renamed target shadows newName for any reference to it that
//	           sits within the target's scope.
func checkNoShadowCapture(fset *token.FileSet, pkgs []*packages.Package, target types.Object, newName string) error {
	targetScope := target.Parent()
	if targetScope == nil {
		// Fields, methods and interface members carry no lexical scope;
		// shadow capture does not apply to them.
		return nil
	}

	for _, p := range pkgs {
		if p.Types == nil || p.TypesInfo == nil {
			continue
		}
		pkgScope := p.Types.Scope()

		// Inbound: for each reference to the target, resolve newName at
		// that position. If it already binds to an object declared inside
		// the target's scope, the rename would capture that reference.
		for id, obj := range p.TypesInfo.Uses {
			if !renameSameObject(fset, obj, target) {
				continue
			}
			inner := pkgScope.Innermost(id.Pos())
			if inner == nil {
				continue
			}
			sc, other := inner.LookupParent(newName, id.Pos())
			if other == nil || other == target {
				continue
			}
			if scopeIsDescendant(sc, targetScope) {
				return fmt.Errorf("cannot rename %q → %q: an existing %q declared in a nested scope would capture a reference to %q at %s",
					target.Name(), newName, newName, target.Name(), fset.Position(id.Pos()))
			}
		}

		// Outbound: the renamed target would shadow an enclosing-scope
		// object named newName for any reference to that object located
		// inside the target's scope.
		for id, obj := range p.TypesInfo.Uses {
			if obj == nil || obj.Name() != newName || obj == target {
				continue
			}
			objScope := obj.Parent()
			if objScope == nil {
				continue
			}
			if !scopeIsDescendant(targetScope, objScope) {
				continue // newName not visible throughout the target's scope
			}
			if scopeContainsPos(targetScope, id.Pos()) {
				return fmt.Errorf("cannot rename %q → %q: the renamed symbol would shadow the enclosing %q and capture a reference to it at %s",
					target.Name(), newName, newName, fset.Position(id.Pos()))
			}
		}
	}
	return nil
}

// scopeIsDescendant reports whether ancestor is a strict ancestor of s
// (i.e. s is nested inside ancestor). Both scopes are assumed to lie on
// the same lexical chain, which holds when they both enclose a common
// position.
func scopeIsDescendant(s, ancestor *types.Scope) bool {
	for s != nil {
		s = s.Parent()
		if s == ancestor {
			return true
		}
	}
	return false
}

// scopeContainsPos reports whether pos falls within scope's source
// extent. Package and file scopes carry no valid position range; they
// enclose the whole package, so we treat them as containing everything.
func scopeContainsPos(scope *types.Scope, pos token.Pos) bool {
	if scope == nil {
		return false
	}
	sp, ep := scope.Pos(), scope.End()
	if !sp.IsValid() || !ep.IsValid() {
		return true
	}
	return sp <= pos && pos < ep
}

// isValidGoIdent reports whether s is a legal Go identifier: starts
// with a letter or underscore, followed by letters/digits/underscores.
// We deliberately don't reject keywords here — the type-checker will,
// and surfacing that failure with the real context is more informative
// than a synthetic error.
func isValidGoIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return false
			}
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

// isExportedIdent mirrors ast.IsExported without depending on it, so
// we can handle unicode-first identifiers correctly.
func isExportedIdent(s string) bool {
	if s == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r)
}
