package commands

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// PatchInterface applies one or more granular patches to an interface's method
// list. All patches are resolved against the original element list, then
// applied atomically. When the patched op changes the method set and
// mock_file / mock_name are provided, the mock is regenerated.
func (h *ExecutePlanHandler) PatchInterface(ctx context.Context, req domain.PatchInterfaceRequest) (domain.PatchInterfaceResult, error) {
	src, err := h.fs.ReadFile(ctx, req.FilePath)
	if err != nil {
		return domain.PatchInterfaceResult{}, &domain.Error{Code: "READ_ERROR", Message: "failed to read file", Err: err}
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, req.FilePath, src, parser.ParseComments)
	if err != nil {
		return domain.PatchInterfaceResult{}, &domain.Error{Code: "PARSE_ERROR", Message: "failed to parse file", Err: err}
	}

	typeSpec, ifaceType, ok := findTargetInterface(f, req.Identifier)
	if !ok {
		return domain.PatchInterfaceResult{}, &domain.Error{
			Code:    "NODE_NOT_FOUND",
			Message: fmt.Sprintf("interface %q not found in %s", req.Identifier, req.FilePath),
		}
	}

	elements := parseInterfaceMethods(fset, src, ifaceType)

	working := make([]*element, len(elements))
	copy(working, elements)

	methodSetChanged := false
	var errs []string
	for i, p := range req.Patches {
		msg, changedSet := applyInterfacePatch(&working, elements, p)
		if msg != "" {
			errs = append(errs, fmt.Sprintf("patch #%d (%s): %s", i+1, p.Op, msg))
			continue
		}
		if changedSet {
			methodSetChanged = true
		}
	}

	if len(errs) > 0 {
		msg := strings.Join(errs, "\n")
		// Include the full interface definition with line numbers so the
		// agent can see current methods and retry without a symbol call.
		startOff := fset.Position(typeSpec.Pos()).Offset
		for startOff > 0 && src[startOff-1] != '\n' {
			startOff--
		}
		startLine := fset.Position(typeSpec.Pos()).Line
		endLine := fset.Position(ifaceType.End()).Line
		endOff := fset.Position(ifaceType.End()).Offset
		if body := formatNumberedSource(src, startOff, endOff, startLine); body != "" {
			msg += fmt.Sprintf("\n\nCurrent definition of %s (lines %d-%d):\n%s", req.Identifier, startLine, endLine, body)
		}
		msg += "\nHint: use the line numbers and member names above to correct your patch."
		return domain.PatchInterfaceResult{}, &domain.Error{
			Code:    "PATCH_FAILED",
			Message: msg,
		}
	}

	lbraceOff := fset.Position(ifaceType.Methods.Opening).Offset
	rbraceOff := fset.Position(ifaceType.Methods.Closing).Offset
	indent := detectStructIndent(src, lbraceOff, rbraceOff)

	newBody := renderElements(working, indent, src)

	newSrc := make([]byte, 0, len(src)+len(newBody))
	newSrc = append(newSrc, src[:lbraceOff+1]...)
	newSrc = append(newSrc, []byte("\n"+newBody+"\n")...)
	newSrc = append(newSrc, src[rbraceOff:]...)

	// Reject the patch before writing if it would produce invalid Go.
	if err := validateGoSource(req.FilePath, newSrc); err != nil {
		return domain.PatchInterfaceResult{}, err
	}

	diff := diffStrings(req.FilePath, string(src), string(newSrc))

	if req.Preview {
		return domain.PatchInterfaceResult{Diff: diff, Applied: len(req.Patches), Preview: true}, nil
	}

	addedImports, err := h.fs.WriteFile(ctx, req.FilePath, newSrc)
	if err != nil {
		return domain.PatchInterfaceResult{}, &domain.Error{Code: "WRITE_ERROR", Message: "failed to write file", Err: err}
	}

	// Regenerate mock if requested and the method set changed.
	mockUpdated := false
	if methodSetChanged && req.MockFile != "" && req.MockName != "" {
		// Extract the new interface source (from the rewritten file) and pass to MockFromSource.
		newInterfaceSrc := extractInterfaceSource(newSrc, typeSpec.Name.Name)
		if newInterfaceSrc != "" {
			if _, mockErr := h.MockFromSource(ctx, newInterfaceSrc, req.MockName, req.MockFile, req.FilePath); mockErr != nil {
				return domain.PatchInterfaceResult{}, fmt.Errorf("patch applied but mock regeneration failed: %w", mockErr)
			}
			mockUpdated = true
		}
	}

	return domain.PatchInterfaceResult{
		Diff:         diff,
		Applied:      len(req.Patches),
		MockUpdated:  mockUpdated,
		AddedImports: addedImports,
	}, nil
}

// findTargetInterface locates the target interface declaration by name.
func findTargetInterface(f *ast.File, identifier string) (*ast.TypeSpec, *ast.InterfaceType, bool) {
	pkgTarget, nameTarget := parseIdentifier(identifier)
	if pkgTarget != "" && pkgTarget != f.Name.Name {
		return nil, nil, false
	}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != nameTarget {
				continue
			}
			it, ok := ts.Type.(*ast.InterfaceType)
			if !ok {
				continue
			}
			return ts, it, true
		}
	}
	return nil, nil, false
}

// parseInterfaceMethods walks the interface's method list into an element slice.
// Entries are either methods (have a Name and a FuncType) or embeds (no name,
// just a type expression).
func parseInterfaceMethods(fset *token.FileSet, src []byte, it *ast.InterfaceType) []*element {
	var out []*element
	for _, field := range it.Methods.List {
		var doc string
		if field.Doc != nil {
			doc = extractDocText(field.Doc)
		}
		var inlineComment string
		if field.Comment != nil {
			inlineComment = extractDocText(field.Comment)
		}
		rawStart := field.Pos()
		if field.Doc != nil {
			rawStart = field.Doc.Pos()
		}
		rawEnd := field.End()
		if field.Comment != nil {
			rawEnd = field.Comment.End()
		}
		rawLine := extractLineRange(src, fset, rawStart, rawEnd)

		if len(field.Names) == 0 {
			// Embedded interface.
			typeExpr := extractSourceRange(src, fset, field.Type.Pos(), field.Type.End())
			out = append(out, &element{
				name:          typeExpr,
				kind:          "embed",
				typeExpr:      typeExpr,
				doc:           doc,
				inlineComment: inlineComment,
				rawLine:       rawLine,
			})
			continue
		}

		// Method. The type is a *ast.FuncType; we store the signature as
		// "(params) results" (everything after the method name).
		funcType, ok := field.Type.(*ast.FuncType)
		if !ok {
			continue
		}
		// Grab the signature text from '(' onwards, relative to src.
		sig := extractFuncSignature(src, fset, funcType)

		// An interface method field can declare multiple names (rare but legal).
		for _, n := range field.Names {
			out = append(out, &element{
				name:          n.Name,
				kind:          "method",
				typeExpr:      sig,
				doc:           doc,
				inlineComment: inlineComment,
				rawLine:       rawLine,
			})
			if len(field.Names) > 1 {
				out[len(out)-1].dirty = true
				out[len(out)-1].rawLine = ""
			}
		}
	}
	return out
}

// extractFuncSignature returns the "(params) results" portion of a FuncType,
// without the leading "func" keyword or any method name.
func extractFuncSignature(src []byte, fset *token.FileSet, ft *ast.FuncType) string {
	start := fset.Position(ft.Params.Pos()).Offset
	var end int
	if ft.Results != nil {
		end = fset.Position(ft.Results.End()).Offset
	} else {
		end = fset.Position(ft.Params.End()).Offset
	}
	if start < 0 || end > len(src) || start >= end {
		return "()"
	}
	return string(src[start:end])
}

// applyInterfacePatch mutates *working in place. Returns (errorMsg, methodSetChanged).
func applyInterfacePatch(working *[]*element, original []*element, p domain.InterfacePatch) (string, bool) {
	switch p.Op {
	case domain.InterfacePatchOpAddMethod:
		if p.Signature == "" {
			return "signature is required", false
		}
		name, sig, err := parseMethodSignature(p.Signature)
		if err != nil {
			return err.Error(), false
		}
		if findElement(*working, name) != -1 {
			return fmt.Sprintf("method %q already exists. Current members: %s", name, listNames(*working)), false
		}
		newElem := &element{
			name:     name,
			kind:     "method",
			typeExpr: sig,
			doc:      p.Doc,
			dirty:    true,
		}
		if msg := insertElement(working, original, p.Before, p.After, p.Position, newElem); msg != "" {
			return msg, false
		}
		return "", true

	case domain.InterfacePatchOpRemoveMethod:
		if p.Name == "" {
			return "name is required", false
		}
		idx := findElement(*working, p.Name)
		if idx == -1 {
			return fmt.Sprintf("method %q not found. Current members: %s", p.Name, listNames(*working)), false
		}
		if (*working)[idx].kind != "method" {
			return fmt.Sprintf("%q is not a method (kind=%s); use remove_embed for embedded interfaces", p.Name, (*working)[idx].kind), false
		}
		*working = append((*working)[:idx], (*working)[idx+1:]...)
		return "", true

	case domain.InterfacePatchOpRenameMethod:
		if p.From == "" || p.To == "" {
			return "from and to are required", false
		}
		idx := findElement(*working, p.From)
		if idx == -1 {
			return fmt.Sprintf("method %q not found. Current members: %s", p.From, listNames(*working)), false
		}
		if (*working)[idx].kind != "method" {
			return fmt.Sprintf("%q is not a method", p.From), false
		}
		if findElement(*working, p.To) != -1 {
			return fmt.Sprintf("method %q already exists — cannot rename %q to a colliding name", p.To, p.From), false
		}
		(*working)[idx].name = p.To
		(*working)[idx].dirty = true
		return "", true

	case domain.InterfacePatchOpRetypeMethod:
		if p.Name == "" || p.Signature == "" {
			return "name and signature are required", false
		}
		idx := findElement(*working, p.Name)
		if idx == -1 {
			return fmt.Sprintf("method %q not found. Current members: %s", p.Name, listNames(*working)), false
		}
		sigName, sig, err := parseMethodSignature(p.Signature)
		if err != nil {
			return err.Error(), false
		}
		if sigName != p.Name {
			return fmt.Sprintf("method name %q in signature does not match name=%q", sigName, p.Name), false
		}
		(*working)[idx].typeExpr = sig
		(*working)[idx].dirty = true
		return "", true

	case domain.InterfacePatchOpSetDoc:
		if p.Name == "" {
			return "name is required", false
		}
		idx := findElement(*working, p.Name)
		if idx == -1 {
			return fmt.Sprintf("member %q not found. Current members: %s", p.Name, listNames(*working)), false
		}
		(*working)[idx].doc = p.Doc
		(*working)[idx].dirty = true
		return "", false

	case domain.InterfacePatchOpEmbed:
		if p.Type == "" {
			return "type is required", false
		}
		if findElement(*working, p.Type) != -1 {
			return fmt.Sprintf("embed %q already present. Current members: %s", p.Type, listNames(*working)), false
		}
		newElem := &element{
			name:     p.Type,
			kind:     "embed",
			typeExpr: p.Type,
			doc:      p.Doc,
			dirty:    true,
		}
		if msg := insertElement(working, original, p.Before, p.After, p.Position, newElem); msg != "" {
			return msg, false
		}
		return "", true

	case domain.InterfacePatchOpRemoveEmbed:
		if p.Type == "" {
			return "type is required", false
		}
		idx := findElement(*working, p.Type)
		if idx == -1 {
			return fmt.Sprintf("embed %q not found. Current members: %s", p.Type, listNames(*working)), false
		}
		if (*working)[idx].kind != "embed" {
			return fmt.Sprintf("%q is not an embedded interface", p.Type), false
		}
		*working = append((*working)[:idx], (*working)[idx+1:]...)
		return "", true

	default:
		return fmt.Sprintf("unknown op %q", p.Op), false
	}
}

// parseMethodSignature parses a full interface method signature like
//
//	"Close() error"
//	"Read(p []byte) (int, error)"
//
// and returns the method name plus the remaining signature ("(p []byte) (int, error)").
func parseMethodSignature(src string) (name string, sig string, err error) {
	wrapped := "package p\ntype i interface {\n" + src + "\n}\n"
	fset := token.NewFileSet()
	f, parseErr := parser.ParseFile(fset, "", wrapped, 0)
	if parseErr != nil {
		return "", "", fmt.Errorf("invalid signature %q: %w", src, parseErr)
	}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			it, ok := ts.Type.(*ast.InterfaceType)
			if !ok {
				continue
			}
			if len(it.Methods.List) == 0 {
				continue
			}
			field := it.Methods.List[0]
			if len(field.Names) == 0 {
				return "", "", fmt.Errorf("signature %q has no method name", src)
			}
			funcType, ok := field.Type.(*ast.FuncType)
			if !ok {
				return "", "", fmt.Errorf("signature %q is not a function type", src)
			}
			// Rebuild sig from the original src by finding the method name.
			methodName := field.Names[0].Name
			after := strings.TrimSpace(src)
			// Skip leading method name.
			if strings.HasPrefix(after, methodName) {
				after = strings.TrimSpace(after[len(methodName):])
			}
			_ = funcType
			return methodName, after, nil
		}
	}
	return "", "", fmt.Errorf("could not parse signature %q", src)
}

// extractInterfaceSource returns the "type Name interface { ... }" source
// for the given interface name from src. Returns "" if not found.
func extractInterfaceSource(src []byte, name string) string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return ""
	}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != name {
				continue
			}
			if _, ok := ts.Type.(*ast.InterfaceType); !ok {
				continue
			}
			start := fset.Position(gen.Pos()).Offset
			end := fset.Position(gen.End()).Offset
			return string(src[start:end])
		}
	}
	return ""
}
