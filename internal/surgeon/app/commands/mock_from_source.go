package commands

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
)

// astMethod holds extracted method info from an interface AST.
type astMethod struct {
	Name       string
	ParamsSrc  string // "(ctx context.Context, id string)"
	ResultsSrc string // "error" or "(int, error)"
	CallArgs   string // "ctx, id"
}

// MockFromSource generates a mock struct from raw interface source code using go/ast.
// interfaceSource is the raw type declaration (without package clause).
// mockName is the name of the mock struct to generate.
// mockFile is the target file path for the mock.
// interfaceFilePath is the file that will hold the interface (used to detect package name).
func (h *ExecutePlanHandler) MockFromSource(ctx context.Context, interfaceSource, mockName, mockFile, interfaceFilePath string) (string, error) {
	wrappedSrc := "package p\n\n" + interfaceSource
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", wrappedSrc, parser.ParseComments)
	if err != nil {
		return "", fmt.Errorf("failed to parse interface source: %w", err)
	}

	// Find the interface type declaration
	var ifaceName string
	var ifaceType *ast.InterfaceType
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if it, ok := ts.Type.(*ast.InterfaceType); ok {
				ifaceName = ts.Name.Name
				ifaceType = it
				break
			}
		}
		if ifaceType != nil {
			break
		}
	}
	if ifaceType == nil {
		return "", fmt.Errorf("no interface type declaration found in source")
	}

	src := []byte(wrappedSrc)
	methods := extractInterfaceMethods(src, fset, ifaceType)

	// Collect package qualifiers used in the interface (e.g. "domain", "context")
	// and resolve them to import paths via the interface file's own imports.
	usedQualifiers := collectQualifiers(src, fset, ifaceType)
	resolvedImports := resolveQualifiersFromFile(ctx, h.fs, interfaceFilePath, usedQualifiers)

	// Detect package names
	mockPkg := h.detectPackageName(ctx, mockFile)
	ifacePkg := h.detectPackageName(ctx, interfaceFilePath)

	receiverName := strings.TrimPrefix(mockName, "*")

	// Build the new mock body (struct + methods + assertion).
	var body bytes.Buffer

	fmt.Fprintf(&body, "type %s struct {\n", receiverName)
	for _, m := range methods {
		fmt.Fprintf(&body, "\t%sFunc func%s %s\n", m.Name, m.ParamsSrc, m.ResultsSrc)
	}
	body.WriteString("}\n")

	for _, m := range methods {
		fmt.Fprintf(&body, "\nfunc (m *%s) %s%s %s {\n", receiverName, m.Name, m.ParamsSrc, m.ResultsSrc)
		fmt.Fprintf(&body, "\tif m.%sFunc == nil {\n", m.Name)
		fmt.Fprintf(&body, "\t\tpanic(\"%s.%sFunc not set\")\n", receiverName, m.Name)
		body.WriteString("\t}\n")
		if m.ResultsSrc != "" {
			fmt.Fprintf(&body, "\treturn m.%sFunc(%s)\n", m.Name, m.CallArgs)
		} else {
			fmt.Fprintf(&body, "\tm.%sFunc(%s)\n", m.Name, m.CallArgs)
		}
		body.WriteString("}\n")
	}

	body.WriteByte('\n')
	if mockPkg == ifacePkg {
		fmt.Fprintf(&body, "var _ %s = (*%s)(nil)\n", ifaceName, receiverName)
	} else {
		fmt.Fprintf(&body, "var _ %s.%s = (*%s)(nil)\n", ifacePkg, ifaceName, receiverName)
	}

	dir := filepath.Dir(mockFile)
	if err := h.fs.MkdirAll(ctx, dir); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	// If the file already exists, surgically replace only this mock's declarations
	// to preserve any sibling mocks in the same file.
	existingSrc, readErr := h.fs.ReadFile(ctx, mockFile)
	if readErr == nil {
		updated, handled, mergeErr := replaceMockInFile(existingSrc, mockFile, receiverName, body.Bytes(), resolvedImports)
		if mergeErr != nil {
			return "", fmt.Errorf("failed to update mock in existing file: %w", mergeErr)
		}
		if handled {
			if _, err := h.fs.WriteFile(ctx, mockFile, updated); err != nil {
				return "", fmt.Errorf("failed to write mock file: %w", err)
			}
			return fmt.Sprintf("Updated %s (%d methods) in %s", receiverName, len(methods), mockFile), nil
		}
		// File exists but has no real declarations — fall through to full write.
	}

	// File does not exist yet (or has no real declarations) — write from scratch.
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "package %s\n\n", mockPkg)
	if len(resolvedImports) > 0 {
		paths := make([]string, 0, len(resolvedImports))
		for p := range resolvedImports {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		buf.WriteString("import (\n")
		for _, p := range paths {
			alias := resolvedImports[p]
			lastSeg := p
			if i := strings.LastIndex(p, "/"); i >= 0 {
				lastSeg = p[i+1:]
			}
			if alias != "" && alias != lastSeg {
				fmt.Fprintf(&buf, "\t%s %q\n", alias, p)
			} else {
				fmt.Fprintf(&buf, "\t%q\n", p)
			}
		}
		buf.WriteString(")\n\n")
	}
	buf.Write(body.Bytes())

	if _, err := h.fs.WriteFile(ctx, mockFile, buf.Bytes()); err != nil {
		return "", fmt.Errorf("failed to write mock file: %w", err)
	}
	return fmt.Sprintf("Generated %s (%d methods) in %s", receiverName, len(methods), mockFile), nil
}

// extractInterfaceMethods extracts method info from an interface AST node.
// Embedded interfaces (unnamed fields) are skipped.
func extractInterfaceMethods(src []byte, fset *token.FileSet, iface *ast.InterfaceType) []astMethod {
	var methods []astMethod
	if iface.Methods == nil {
		return methods
	}

	for _, field := range iface.Methods.List {
		// Skip embedded interfaces (no names)
		if len(field.Names) == 0 {
			continue
		}
		ft, ok := field.Type.(*ast.FuncType)
		if !ok {
			continue
		}
		name := field.Names[0].Name
		paramsSrc, callArgs := extractParams(src, fset, ft)
		resultsSrc := extractResults(src, fset, ft)
		methods = append(methods, astMethod{
			Name:       name,
			ParamsSrc:  paramsSrc,
			ResultsSrc: resultsSrc,
			CallArgs:   callArgs,
		})
	}
	return methods
}

// extractParams returns the formatted parameter list "(p type, ...)" and call args "p, ...".
func extractParams(src []byte, fset *token.FileSet, ft *ast.FuncType) (string, string) {
	if ft.Params == nil || len(ft.Params.List) == 0 {
		return "()", ""
	}

	var paramParts []string
	var argParts []string
	unnamed := 0

	for i, field := range ft.Params.List {
		typeSrc := nodeSource(src, fset, field.Type)
		isVariadic := isVariadicField(field, ft, i)
		if isVariadic {
			// *ast.Ellipsis source already reads "...T"; strip the prefix so
			// the single "..." added below isn't doubled.
			typeSrc = strings.TrimPrefix(typeSrc, "...")
		}

		if len(field.Names) == 0 {
			// Unnamed parameter
			name := fmt.Sprintf("p%d", unnamed)
			unnamed++
			if isVariadic {
				// variadic: type is "[]T" in source as "...T"
				paramParts = append(paramParts, fmt.Sprintf("%s ...%s", name, typeSrc))
				argParts = append(argParts, name+"...")
			} else {
				paramParts = append(paramParts, fmt.Sprintf("%s %s", name, typeSrc))
				argParts = append(argParts, name)
			}
		} else {
			for _, ident := range field.Names {
				name := ident.Name
				if isVariadic {
					paramParts = append(paramParts, fmt.Sprintf("%s ...%s", name, typeSrc))
					argParts = append(argParts, name+"...")
				} else {
					paramParts = append(paramParts, fmt.Sprintf("%s %s", name, typeSrc))
					argParts = append(argParts, name)
				}
			}
		}
	}

	return "(" + strings.Join(paramParts, ", ") + ")", strings.Join(argParts, ", ")
}

// isVariadicField reports whether the field at index i is a variadic parameter.
func isVariadicField(field *ast.Field, ft *ast.FuncType, i int) bool {
	if !ft.Params.List[len(ft.Params.List)-1].Pos().IsValid() {
		return false
	}
	if i != len(ft.Params.List)-1 {
		return false
	}
	_, ok := field.Type.(*ast.Ellipsis)
	return ok
}

// extractResults returns the formatted result type string, e.g. "error", "(int, error)", or "".
func extractResults(src []byte, fset *token.FileSet, ft *ast.FuncType) string {
	if ft.Results == nil || len(ft.Results.List) == 0 {
		return ""
	}
	if len(ft.Results.List) == 1 && len(ft.Results.List[0].Names) == 0 {
		return nodeSource(src, fset, ft.Results.List[0].Type)
	}
	// Multiple results or named results: extract raw source between parens
	start := fset.Position(ft.Results.Opening).Offset
	end := fset.Position(ft.Results.Closing).Offset + 1
	if ft.Results.Opening.IsValid() && ft.Results.Closing.IsValid() {
		return string(src[start:end])
	}
	// Fallback: build manually
	var parts []string
	for _, field := range ft.Results.List {
		typeSrc := nodeSource(src, fset, field.Type)
		if len(field.Names) == 0 {
			parts = append(parts, typeSrc)
		} else {
			for _, n := range field.Names {
				parts = append(parts, n.Name+" "+typeSrc)
			}
		}
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// nodeSource extracts raw source text for an AST node.
func nodeSource(src []byte, fset *token.FileSet, node ast.Node) string {
	start := fset.Position(node.Pos()).Offset
	end := fset.Position(node.End()).Offset
	if start < 0 || end > len(src) || start >= end {
		return ""
	}
	return string(src[start:end])
}

// collectQualifiers walks the interface AST and returns the set of package
// qualifier names used in parameter/result types (e.g. "context", "domain").
func collectQualifiers(src []byte, fset *token.FileSet, iface *ast.InterfaceType) map[string]bool {
	qualifiers := map[string]bool{}
	ast.Inspect(iface, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok {
			qualifiers[id.Name] = true
		}
		return true
	})
	return qualifiers
}

// resolveQualifiersFromFile reads interfaceFilePath and maps each qualifier
// name in usedQualifiers to its import path. Returns a map of
// importPath -> localName (alias or last segment).
func resolveQualifiersFromFile(ctx context.Context, fs interface {
	ReadFile(context.Context, string) ([]byte, error)
}, interfaceFilePath string, usedQualifiers map[string]bool) map[string]string {
	result := map[string]string{}
	if len(usedQualifiers) == 0 {
		return result
	}
	src, err := fs.ReadFile(ctx, interfaceFilePath)
	if err != nil {
		return result
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, interfaceFilePath, src, 0)
	if err != nil {
		return result
	}
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		lastSeg := path
		if i := strings.LastIndex(path, "/"); i >= 0 {
			lastSeg = path[i+1:]
		}
		localName := lastSeg
		if imp.Name != nil && imp.Name.Name != "_" && imp.Name.Name != "." {
			localName = imp.Name.Name
		}
		if usedQualifiers[localName] {
			result[path] = localName
		}
	}
	return result
}

// replaceMockInFile surgically replaces the named mock's struct, methods, and
// compile-time assertion in existingSrc, then merges any new imports into the
// file's import block. Returns the updated file bytes.
// replaceMockInFile surgically replaces the named mock's struct, methods, and
// compile-time assertion in existingSrc, then merges any new imports into the
// file's import block. Returns the updated file bytes and true when the mock
// existed (surgical update); returns nil, false when the mock was absent but
// other declarations are present (caller should append). Returns nil, false
// when the file has no real declarations (caller should overwrite from scratch).
func replaceMockInFile(existingSrc []byte, mockFile, receiverName string, newBody []byte, newImports map[string]string) ([]byte, bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, mockFile, existingSrc, parser.ParseComments)
	if err != nil {
		return nil, false, fmt.Errorf("parse existing mock file: %w", err)
	}

	structRanges := findStructAndMethodsOffsets(fset, f, receiverName)

	if len(structRanges) == 0 {
		// Mock not found in this file. If the file has other real declarations
		// (sibling mocks), preserve them and append the new body.
		// If the file has no declarations at all, signal caller to overwrite.
		hasDecls := false
		for _, decl := range f.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				hasDecls = true
			case *ast.GenDecl:
				if decl.Tok == token.TYPE || decl.Tok == token.VAR || decl.Tok == token.CONST {
					hasDecls = true
				}
			}
		}
		if !hasDecls {
			return nil, false, nil
		}
		// Append new body to existing content.
		updated := bytes.TrimRight(existingSrc, " \t\n")
		updated = append(updated, '\n', '\n')
		updated = append(updated, newBody...)
		if len(newImports) > 0 {
			updated, err = mergeImports(updated, mockFile, newImports)
			if err != nil {
				return nil, false, err
			}
		}
		return updated, true, nil
	}

	var delRanges [][2]int
	delRanges = append(delRanges, structRanges...)
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		if !isMockAssertion(gen, receiverName) {
			continue
		}
		delRanges = append(delRanges, [2]int{
			fset.Position(gen.Pos()).Offset,
			fset.Position(gen.End()).Offset,
		})
	}

	updated := deleteRanges(existingSrc, delRanges)
	updated = bytes.TrimRight(updated, " \t\n")
	updated = append(updated, '\n', '\n')
	updated = append(updated, newBody...)

	if len(newImports) > 0 {
		updated, err = mergeImports(updated, mockFile, newImports)
		if err != nil {
			return nil, false, err
		}
	}
	return updated, true, nil
}

// mergeImports adds any import paths from toAdd that are not already present
// in the file's import block. Returns the updated source.
func mergeImports(src []byte, filename string, toAdd map[string]string) ([]byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return src, nil
	}

	existing := map[string]bool{}
	for _, imp := range f.Imports {
		existing[strings.Trim(imp.Path.Value, `"`)] = true
	}

	var newPaths []string
	for path := range toAdd {
		if !existing[path] {
			newPaths = append(newPaths, path)
		}
	}
	if len(newPaths) == 0 {
		return src, nil
	}
	sort.Strings(newPaths)

	var snippet strings.Builder
	for _, p := range newPaths {
		alias := toAdd[p]
		lastSeg := p
		if i := strings.LastIndex(p, "/"); i >= 0 {
			lastSeg = p[i+1:]
		}
		if alias != "" && alias != lastSeg {
			fmt.Fprintf(&snippet, "\t%s %q\n", alias, p)
		} else {
			fmt.Fprintf(&snippet, "\t%q\n", p)
		}
	}

	if len(f.Imports) > 0 {
		var insertOffset int
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.IMPORT || !gen.Lparen.IsValid() {
				continue
			}
			off := fset.Position(gen.Rparen).Offset
			if off > insertOffset {
				insertOffset = off
			}
		}
		if insertOffset > 0 {
			result := make([]byte, 0, len(src)+len(snippet.String()))
			result = append(result, src[:insertOffset]...)
			result = append(result, snippet.String()...)
			result = append(result, src[insertOffset:]...)
			return result, nil
		}
	}

	pkgEnd := fset.Position(f.Name.End()).Offset
	rest := src[pkgEnd:]
	newlineOff := bytes.IndexByte(rest, '\n')
	if newlineOff < 0 {
		newlineOff = len(rest)
	}
	insertAt := pkgEnd + newlineOff + 1
	block := fmt.Sprintf("\nimport (\n%s)\n", snippet.String())
	result := make([]byte, 0, len(src)+len(block))
	result = append(result, src[:insertAt]...)
	result = append(result, block...)
	result = append(result, src[insertAt:]...)
	return result, nil
}
