package commands

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"golang.org/x/tools/go/packages"
)

// Implement generates and appends missing interface methods to a struct.
func (h *ExecutePlanHandler) Implement(ctx context.Context, req domain.ImplementRequest) ([]domain.SymbolResult, error) {
	if req.Interface == "" || req.Receiver == "" || req.FilePath == "" {
		return nil, fmt.Errorf("interface, receiver, and file path are required")
	}

	// 1. Resolve the interface using go/packages
	iface, err := resolveInterface(req.Interface)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve interface: %w", err)
	}

	// 2. Read the target file
	src, err := h.fs.ReadFile(ctx, req.FilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", req.FilePath, err)
	}

	// 3. Find existing methods in the entire package/directory to avoid duplicates
	targetDir := filepath.Dir(req.FilePath)
	receiverBaseName := strings.TrimPrefix(req.Receiver, "*")
	existingMethods := make(map[string]*ast.FuncDecl)

	entries, err := h.fs.ReadDir(ctx, targetDir)
	if err == nil {
		fsetDir := token.NewFileSet()
		for _, entry := range entries {
			if !strings.HasSuffix(entry, ".go") {
				continue
			}
			path := filepath.Join(targetDir, entry)
			content, err := h.fs.ReadFile(ctx, path)
			if err != nil {
				continue
			}
			f, err := parser.ParseFile(fsetDir, path, content, 0)
			if err != nil {
				continue
			}
			for _, decl := range f.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv != nil {
					for _, field := range fn.Recv.List {
						var typeIdent *ast.Ident
						switch t := field.Type.(type) {
						case *ast.Ident:
							typeIdent = t
						case *ast.StarExpr:
							if ident, ok := t.X.(*ast.Ident); ok {
								typeIdent = ident
							}
						}
						if typeIdent != nil && typeIdent.Name == receiverBaseName {
							existingMethods[fn.Name.Name] = fn
						}
					}
				}
			}
		}
	}

	// 4. Generate the missing methods
	var newContent bytes.Buffer
	var generatedMethodNames []string
	qualifier := func(p *types.Package) string {
		return p.Name()
	}

	for i := 0; i < iface.NumMethods(); i++ {
		m := iface.Method(i)
		sig := m.Type().(*types.Signature)

		if existingFn, ok := existingMethods[m.Name()]; ok {
			// Basic signature validation: verify parameter and result counts
			astParams := 0
			if existingFn.Type.Params != nil {
				for _, field := range existingFn.Type.Params.List {
					if len(field.Names) > 0 {
						astParams += len(field.Names)
					} else {
						astParams += 1
					}
				}
			}

			astResults := 0
			if existingFn.Type.Results != nil {
				for _, field := range existingFn.Type.Results.List {
					if len(field.Names) > 0 {
						astResults += len(field.Names)
					} else {
						astResults += 1
					}
				}
			}

			if astParams != sig.Params().Len() || astResults != sig.Results().Len() {
				return nil, fmt.Errorf("conflict: method '%s' already exists on %s but signature mismatches! Expected %d params and %d results, got %d params and %d results", m.Name(), receiverBaseName, sig.Params().Len(), sig.Results().Len(), astParams, astResults)
			}

			continue // Already implemented (counts match)
		}
		
		params := types.TypeString(sig.Params(), qualifier)
		results := types.TypeString(sig.Results(), qualifier)
		
		if results != "" && !strings.HasPrefix(results, "(") {
			// e.g. "error" becomes "(error)", but go/types TypeString might just return "error"
			// Actually, let's keep it simple, but we need to know what TypeString returns.
			// TypesString on a Tuple returns "(x int, y string)"
		}

		methodStr := fmt.Sprintf("\n// %s implements %s.\nfunc (r %s) %s%s %s {\n\t// TODO: implement %s\n\tpanic(\"not implemented\")\n}\n",
			m.Name(), req.Interface, req.Receiver, m.Name(), params, results, m.Name())
		newContent.WriteString(methodStr)
		generatedMethodNames = append(generatedMethodNames, m.Name())
	}

	if newContent.Len() == 0 {
		return nil, nil // Nothing to generate
	}

	// 5. Append to the file
	updatedSrc := append(src, newContent.Bytes()...)

	// Format code
	formattedSrc, err := format.Source(updatedSrc)
	if err == nil {
		updatedSrc = formattedSrc
	}

	if err := h.fs.WriteFile(ctx, req.FilePath, updatedSrc); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	// 6. Re-parse the updated file to extract SymbolResults
	fsetNew := token.NewFileSet()
	fNew, err := parser.ParseFile(fsetNew, req.FilePath, updatedSrc, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to re-parse file: %w", err)
	}

	var results []domain.SymbolResult
	for _, decl := range fNew.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv != nil {
			// check if it's one of the newly generated methods
			isNew := false
			for _, name := range generatedMethodNames {
				if fn.Name.Name == name {
					isNew = true
					break
				}
			}
			if !isNew {
				continue
			}

			// Extract SymbolResult
			res := extractFuncResult(fsetNew, updatedSrc, fn, req.FilePath, h.ReceiverBaseName(req.Receiver))
			results = append(results, res)
		}
	}

	return results, nil
}

func (h *ExecutePlanHandler) ReceiverBaseName(receiver string) string {
	return strings.TrimPrefix(receiver, "*")
}

func resolveInterface(ifacePath string) (*types.Interface, error) {
	parts := strings.Split(ifacePath, ".")
	var pkgPath, name string
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid interface path (expected package.Interface): %s", ifacePath)
	}
	
	name = parts[len(parts)-1]
	pkgPath = strings.Join(parts[:len(parts)-1], ".")

	cfg := &packages.Config{Mode: packages.NeedTypes | packages.NeedImports | packages.NeedDeps}
	pkgs, err := packages.Load(cfg, pkgPath)
	if err != nil {
		return nil, err
	}

	if len(pkgs) == 0 {
		return nil, fmt.Errorf("package %s not found", pkgPath)
	}

	pkg := pkgs[0]
	if len(pkg.Errors) > 0 {
		return nil, pkg.Errors[0]
	}

	obj := pkg.Types.Scope().Lookup(name)
	if obj == nil {
		return nil, fmt.Errorf("symbol %s not found in package %s", name, pkgPath)
	}

	iface, ok := obj.Type().Underlying().(*types.Interface)
	if !ok {
		return nil, fmt.Errorf("%s is not an interface", ifacePath)
	}

	return iface, nil
}

func extractFuncResult(fset *token.FileSet, src []byte, fn *ast.FuncDecl, path, recv string) domain.SymbolResult {
	startPos := fset.Position(fn.Pos())
	endPos := fset.Position(fn.End())

	doc := ""
	if fn.Doc != nil {
		doc = strings.TrimSpace(fn.Doc.Text())
	}

	sigEnd := fn.Body.Pos()
	if sigEnd == token.NoPos {
		sigEnd = fn.End()
	}
	sigBytes := src[startPos.Offset:fset.Position(sigEnd).Offset]
	signature := strings.TrimSpace(string(sigBytes))

	codeLines := strings.Split(string(src[startPos.Offset:endPos.Offset]), "\n")
	var buf bytes.Buffer
	currentLine := startPos.Line
	for _, line := range codeLines {
		if strings.TrimSpace(line) != "" {
			buf.WriteString(fmt.Sprintf("%d: %s\n", currentLine, line))
		}
		currentLine++
	}

	return domain.SymbolResult{
		File:      path,
		LineStart: startPos.Line,
		LineEnd:   endPos.Line,
		Name:      fn.Name.Name,
		Receiver:  recv,
		Signature: signature,
		Doc:       doc,
		Code:      strings.TrimSuffix(buf.String(), "\n"),
	}
}
