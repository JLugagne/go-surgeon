package commands

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
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

	// Detect package names
	mockPkg := h.detectPackageName(ctx, mockFile)
	ifacePkg := h.detectPackageName(ctx, interfaceFilePath)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "package %s\n\n", mockPkg)

	receiverName := strings.TrimPrefix(mockName, "*")

	// Struct with function fields
	fmt.Fprintf(&buf, "type %s struct {\n", receiverName)
	for _, m := range methods {
		fmt.Fprintf(&buf, "\t%sFunc func%s %s\n", m.Name, m.ParamsSrc, m.ResultsSrc)
	}
	buf.WriteString("}\n")

	// Delegation methods
	for _, m := range methods {
		fmt.Fprintf(&buf, "\nfunc (m *%s) %s%s %s {\n", receiverName, m.Name, m.ParamsSrc, m.ResultsSrc)
		fmt.Fprintf(&buf, "\tif m.%sFunc == nil {\n", m.Name)
		fmt.Fprintf(&buf, "\t\tpanic(\"%s.%sFunc not set\")\n", receiverName, m.Name)
		buf.WriteString("\t}\n")
		if m.ResultsSrc != "" {
			fmt.Fprintf(&buf, "\treturn m.%sFunc(%s)\n", m.Name, m.CallArgs)
		} else {
			fmt.Fprintf(&buf, "\tm.%sFunc(%s)\n", m.Name, m.CallArgs)
		}
		buf.WriteString("}\n")
	}

	// Compile-time interface check
	buf.WriteByte('\n')
	if mockPkg == ifacePkg {
		fmt.Fprintf(&buf, "var _ %s = (*%s)(nil)\n", ifaceName, receiverName)
	} else {
		fmt.Fprintf(&buf, "var _ %s.%s = (*%s)(nil)\n", ifacePkg, ifaceName, receiverName)
	}

	// Write file
	dir := filepath.Dir(mockFile)
	if err := h.fs.MkdirAll(ctx, dir); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}
	if err := h.fs.WriteFile(ctx, mockFile, buf.Bytes()); err != nil {
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

		if len(field.Names) == 0 {
			// Unnamed parameter
			name := fmt.Sprintf("p%d", unnamed)
			unnamed++
			if isVariadic {
				// variadic: type is "[]T" in source as "...T"
				paramParts = append(paramParts, fmt.Sprintf("%s ...%s", name, strings.TrimPrefix(typeSrc, "[]")))
				argParts = append(argParts, name+"...")
			} else {
				paramParts = append(paramParts, fmt.Sprintf("%s %s", name, typeSrc))
				argParts = append(argParts, name)
			}
		} else {
			for _, ident := range field.Names {
				name := ident.Name
				if isVariadic {
					paramParts = append(paramParts, fmt.Sprintf("%s ...%s", name, strings.TrimPrefix(typeSrc, "[]")))
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
