package commands

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

func (h *ExecutePlanHandler) ExtractInterface(ctx context.Context, req domain.ExtractInterfaceRequest) (string, error) {
	dir := filepath.Dir(req.FilePath)
	files, err := h.fs.ReadDir(ctx, dir)
	if err != nil {
		return "", &domain.Error{Code: "READ_ERROR", Message: "failed to read directory", Err: err}
	}

	var methods []string

	for _, fileName := range files {
		if !strings.HasSuffix(fileName, ".go") || strings.HasSuffix(fileName, "_test.go") {
			continue
		}

		path := filepath.Join(dir, fileName)
		src, err := h.fs.ReadFile(ctx, path)
		if err != nil {
			return "", err
		}

		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return "", err
		}

		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				if fn.Recv != nil && fn.Name.IsExported() {
					if getRecvType(fn.Recv) == req.StructName {
						var sigBuf bytes.Buffer
						if err := format.Node(&sigBuf, fset, fn.Type); err != nil {
							return "", fmt.Errorf("failed to format signature: %w", err)
						}
						sig := sigBuf.String()
						sig = strings.TrimPrefix(sig, "func")
						sig = strings.TrimSpace(sig)

						methods = append(methods, fmt.Sprintf("\t%s%s", fn.Name.Name, sig))
					}
				}
			}
		}
	}

	if len(methods) == 0 {
		return "", &domain.Error{Code: "NOT_FOUND", Message: fmt.Sprintf("no exported methods found for struct '%s'", req.StructName)}
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "type %s interface {\n", req.InterfaceName)
	for _, m := range methods {
		buf.WriteString(m + "\n")
	}
	buf.WriteString("}")

	interfaceContent := buf.String()

	destPath := req.OutPath
	if destPath == "" {
		destPath = req.FilePath
	}

	// Use AddInterface to handle interface addition and potential mock generation
	actionReq := domain.InterfaceActionRequest{
		FilePath: destPath,
		Content:  interfaceContent,
		MockFile: req.MockFile,
		MockName: req.MockName,
	}

	return h.AddInterface(ctx, actionReq)
}
