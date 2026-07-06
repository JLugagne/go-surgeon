package commands

import (
	"bytes"
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"go/types"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// Mock generates a function-field mock struct that satisfies an interface.
func (h *ExecutePlanHandler) Mock(ctx context.Context, req domain.MockRequest) (string, error) {
	if req.Interface == "" || req.Receiver == "" || req.FilePath == "" {
		return "", fmt.Errorf("interface, receiver, and file path are required")
	}
	if req.Preview {
		child := req
		child.Preview = false
		previewH, _ := h.previewHandler()
		return previewH.Mock(ctx, child)
	}

	// Resolve the interface (cached for MCP sessions)
	resolved, err := h.resolveInterfaceCached(req.Interface)
	if err != nil {
		return "", fmt.Errorf("failed to resolve interface %s: %w", req.Interface, err)
	}

	targetPkg := h.detectPackageName(ctx, req.FilePath)
	receiverName := strings.TrimPrefix(req.Receiver, "*")
	iface := resolved.iface

	// Track every package referenced through the qualifier so we can emit
	// explicit imports. goimports cannot resolve local project packages by
	// short name (e.g. "domain") — only the source interface knows the
	// canonical path, so we must propagate it ourselves. See issue #19.
	targetPkgPath := h.detectPackagePath(ctx, req.FilePath)
	usedPkgs := make(map[string]*types.Package)
	qualifier := func(p *types.Package) string {
		if p == nil {
			return ""
		}
		if targetPkgPath != "" && p.Path() == targetPkgPath {
			return ""
		}
		usedPkgs[p.Path()] = p
		return p.Name()
	}

	var body bytes.Buffer

	fmt.Fprintf(&body, "type %s struct {\n", receiverName)
	for i := 0; i < iface.NumMethods(); i++ {
		m := iface.Method(i)
		sig := m.Type().(*types.Signature)
		fmt.Fprintf(&body, "\t%sFunc %s\n", m.Name(), types.TypeString(sig, qualifier))
	}
	body.WriteString("}\n")

	for i := 0; i < iface.NumMethods(); i++ {
		m := iface.Method(i)
		sig := m.Type().(*types.Signature)

		params, callArgs := buildMockParams(sig, qualifier)
		results := buildMockResults(sig, qualifier)

		fmt.Fprintf(&body, "\nfunc (m *%s) %s%s %s {\n", receiverName, m.Name(), params, results)
		fmt.Fprintf(&body, "\tif m.%sFunc == nil {\n", m.Name())
		fmt.Fprintf(&body, "\t\tpanic(\"%s.%sFunc not set\")\n", receiverName, m.Name())
		body.WriteString("\t}\n")

		if sig.Results().Len() > 0 {
			fmt.Fprintf(&body, "\treturn m.%sFunc(%s)\n", m.Name(), callArgs)
		} else {
			fmt.Fprintf(&body, "\tm.%sFunc(%s)\n", m.Name(), callArgs)
		}
		body.WriteString("}\n")
	}

	body.WriteByte('\n')
	if targetPkg == resolved.pkgName && (targetPkgPath == "" || targetPkgPath == resolved.pkgPath) {
		fmt.Fprintf(&body, "var _ %s = (*%s)(nil)\n", resolved.typeName, receiverName)
	} else {
		fmt.Fprintf(&body, "var _ %s.%s = (*%s)(nil)\n", resolved.pkgName, resolved.typeName, receiverName)
		if resolved.pkg != nil {
			usedPkgs[resolved.pkgPath] = resolved.pkg
		}
	}

	// Build the file: package clause + import block (if any) + body.
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "package %s\n\n", targetPkg)
	if len(usedPkgs) > 0 {
		paths := make([]string, 0, len(usedPkgs))
		for p := range usedPkgs {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		buf.WriteString("import (\n")
		for _, p := range paths {
			pkg := usedPkgs[p]
			// Emit an alias when the package's declared name differs from
			// the last path segment — otherwise the bare path resolves to
			// the wrong selector.
			lastSeg := p
			if i := strings.LastIndex(p, "/"); i >= 0 {
				lastSeg = p[i+1:]
			}
			if pkg.Name() != "" && pkg.Name() != lastSeg {
				fmt.Fprintf(&buf, "\t%s %q\n", pkg.Name(), p)
			} else {
				fmt.Fprintf(&buf, "\t%q\n", p)
			}
		}
		buf.WriteString(")\n\n")
	}
	buf.Write(body.Bytes())

	dir := filepath.Dir(req.FilePath)
	if err := h.fs.MkdirAll(ctx, dir); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	// If the file already exists, surgically replace only this mock's
	// declarations so sibling mocks in the same file survive, mirroring
	// MockFromSource.replaceMockInFile. Without this a second mock into the
	// same file destroyed the first. See issue #17.
	resolvedImports := make(map[string]string, len(usedPkgs))
	for p, pkg := range usedPkgs {
		resolvedImports[p] = pkg.Name()
	}
	if existingSrc, readErr := h.fs.ReadFile(ctx, req.FilePath); readErr == nil {
		updated, handled, mergeErr := replaceMockInFile(existingSrc, req.FilePath, receiverName, body.Bytes(), resolvedImports)
		if mergeErr != nil {
			return "", fmt.Errorf("failed to update mock in existing file: %w", mergeErr)
		}
		if handled {
			if _, err := h.fs.WriteFile(ctx, req.FilePath, updated); err != nil {
				return "", fmt.Errorf("failed to write file: %w", err)
			}
			return fmt.Sprintf("Generated %s with %d methods in %s", receiverName, iface.NumMethods(), req.FilePath), nil
		}
	}

	if _, err := h.fs.WriteFile(ctx, req.FilePath, buf.Bytes()); err != nil {
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

// detectPackagePath returns the canonical import path for the directory
// containing filePath, by shelling out to `go list`. Returns "" if the
// directory does not yet belong to a Go module (e.g. a brand-new test fixture)
// or `go list` fails for any reason.
func (h *ExecutePlanHandler) detectPackagePath(ctx context.Context, filePath string) string {
	dir := filepath.Dir(filePath)
	cmd := exec.CommandContext(ctx, "go", "list", "-f", "{{.ImportPath}}", ".")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
