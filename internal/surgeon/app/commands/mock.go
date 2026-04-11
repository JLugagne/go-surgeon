package commands

import (
	"bytes"
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// Mock generates a function-field mock struct that satisfies an interface.
func (h *ExecutePlanHandler) Mock(ctx context.Context, req domain.MockRequest) (string, error) {
	if req.Interface == "" || req.Receiver == "" || req.FilePath == "" {
		return "", fmt.Errorf("interface, receiver, and file path are required")
	}

	// Resolve the interface (cached for MCP sessions)
	resolved, err := h.resolveInterfaceCached(req.Interface)
	if err != nil {
		return "", fmt.Errorf("failed to resolve interface %s: %w", req.Interface, err)
	}

	targetPkg := h.detectPackageName(ctx, req.FilePath)
	receiverName := strings.TrimPrefix(req.Receiver, "*")
	iface := resolved.iface

	qualifier := func(p *types.Package) string {
		return p.Name()
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "package %s\n\n", targetPkg)

	fmt.Fprintf(&buf, "type %s struct {\n", receiverName)
	for i := 0; i < iface.NumMethods(); i++ {
		m := iface.Method(i)
		sig := m.Type().(*types.Signature)
		fmt.Fprintf(&buf, "\t%sFunc %s\n", m.Name(), types.TypeString(sig, qualifier))
	}
	buf.WriteString("}\n")

	for i := 0; i < iface.NumMethods(); i++ {
		m := iface.Method(i)
		sig := m.Type().(*types.Signature)

		params, callArgs := buildMockParams(sig, qualifier)
		results := buildMockResults(sig, qualifier)

		fmt.Fprintf(&buf, "\nfunc (m *%s) %s%s %s {\n", receiverName, m.Name(), params, results)
		fmt.Fprintf(&buf, "\tif m.%sFunc == nil {\n", m.Name())
		fmt.Fprintf(&buf, "\t\tpanic(\"%s.%sFunc not set\")\n", receiverName, m.Name())
		buf.WriteString("\t}\n")

		if sig.Results().Len() > 0 {
			fmt.Fprintf(&buf, "\treturn m.%sFunc(%s)\n", m.Name(), callArgs)
		} else {
			fmt.Fprintf(&buf, "\tm.%sFunc(%s)\n", m.Name(), callArgs)
		}
		buf.WriteString("}\n")
	}

	buf.WriteByte('\n')
	if targetPkg == resolved.pkgName {
		fmt.Fprintf(&buf, "var _ %s = (*%s)(nil)\n", resolved.typeName, receiverName)
	} else {
		fmt.Fprintf(&buf, "var _ %s.%s = (*%s)(nil)\n", resolved.pkgName, resolved.typeName, receiverName)
	}

	dir := filepath.Dir(req.FilePath)
	if err := h.fs.MkdirAll(ctx, dir); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	if err := h.fs.WriteFile(ctx, req.FilePath, buf.Bytes()); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return fmt.Sprintf("Generated %s with %d methods in %s", receiverName, iface.NumMethods(), req.FilePath), nil
}

// detectPackageName determines the Go package name for the target file's directory.
func (h *ExecutePlanHandler) detectPackageName(ctx context.Context, filePath string) string {
	dir := filepath.Dir(filePath)
	entries, err := h.fs.ReadDir(ctx, dir)
	if err == nil {
		fset := token.NewFileSet()
		for _, entry := range entries {
			if !strings.HasSuffix(entry, ".go") || strings.HasSuffix(entry, "_test.go") {
				continue
			}
			path := filepath.Join(dir, entry)
			content, err := h.fs.ReadFile(ctx, path)
			if err != nil {
				continue
			}
			f, err := parser.ParseFile(fset, path, content, parser.PackageClauseOnly)
			if err != nil {
				continue
			}
			return f.Name.Name
		}
	}
	return filepath.Base(dir)
}

// buildMockParams builds the method parameter list and the forwarding call arguments.
func buildMockParams(sig *types.Signature, qualifier types.Qualifier) (string, string) {
	params := sig.Params()
	var paramParts []string
	var argParts []string

	for i := 0; i < params.Len(); i++ {
		p := params.At(i)
		name := p.Name()
		if name == "" {
			name = fmt.Sprintf("p%d", i)
		}

		typ := p.Type()
		if sig.Variadic() && i == params.Len()-1 {
			if slice, ok := typ.(*types.Slice); ok {
				paramParts = append(paramParts, fmt.Sprintf("%s ...%s", name, types.TypeString(slice.Elem(), qualifier)))
			} else {
				paramParts = append(paramParts, fmt.Sprintf("%s %s", name, types.TypeString(typ, qualifier)))
			}
			argParts = append(argParts, name+"...")
		} else {
			paramParts = append(paramParts, fmt.Sprintf("%s %s", name, types.TypeString(typ, qualifier)))
			argParts = append(argParts, name)
		}
	}

	return "(" + strings.Join(paramParts, ", ") + ")", strings.Join(argParts, ", ")
}

// buildMockResults builds the return type string for a method signature.
func buildMockResults(sig *types.Signature, qualifier types.Qualifier) string {
	results := sig.Results()
	switch results.Len() {
	case 0:
		return ""
	case 1:
		return types.TypeString(results.At(0).Type(), qualifier)
	default:
		return types.TypeString(results, qualifier)
	}
}
