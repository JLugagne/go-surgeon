package queries

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/repositories/filesystem"
)

type SurgeonQueriesHandler struct {
	fs filesystem.FileSystem
}

func NewSurgeonQueriesHandler(fs filesystem.FileSystem) *SurgeonQueriesHandler {
	return &SurgeonQueriesHandler{fs: fs}
}

func (h *SurgeonQueriesHandler) FindSymbols(ctx context.Context, query domain.SymbolQuery, targetDir string) ([]domain.SymbolResult, error) {
	var results []domain.SymbolResult

	err := filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() {
			if info.Name() == "vendor" || (strings.HasPrefix(info.Name(), ".") && path != ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		src, err := h.fs.ReadFile(ctx, path)
		if err != nil {
			return nil
		}

		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			return nil
		}

		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				var recvName string
				if fn.Recv != nil {
					recvName = getRecvType(fn.Recv)
				}

				if fn.Name.Name == query.Name && (query.Receiver == "" || query.Receiver == recvName) {
					results = append(results, h.extractFuncResult(fset, src, fn, path, recvName))
				}
			} else if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.TYPE && query.Receiver == "" {
				for _, spec := range gen.Specs {
					if typeSpec, ok := spec.(*ast.TypeSpec); ok && typeSpec.Name.Name == query.Name {
						results = append(results, h.extractStructResult(fset, src, gen, typeSpec, path))
					}
				}
			}
		}

		return nil
	})

	return results, err
}

func getRecvType(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	switch t := recv.List[0].Type.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return ident.Name
		}
	}
	return ""
}

func (h *SurgeonQueriesHandler) extractFuncResult(fset *token.FileSet, src []byte, fn *ast.FuncDecl, path, recv string) domain.SymbolResult {
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

func (h *SurgeonQueriesHandler) extractStructResult(fset *token.FileSet, src []byte, gen *ast.GenDecl, typeSpec *ast.TypeSpec, path string) domain.SymbolResult {
	startPos := fset.Position(typeSpec.Pos())
	endPos := fset.Position(typeSpec.End())

	doc := ""
	if typeSpec.Doc != nil {
		doc = strings.TrimSpace(typeSpec.Doc.Text())
	} else if len(gen.Specs) == 1 && gen.Doc != nil {
		doc = strings.TrimSpace(gen.Doc.Text())
		startPos = fset.Position(gen.Doc.Pos()) // Include struct group comment start
	}

	sigBytes := src[fset.Position(typeSpec.Pos()).Offset:endPos.Offset]
	signature := strings.TrimSpace(string(sigBytes))
	// For struct, signature is usually just the type definition. 
	// We'll treat the entire spec as signature.

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
		Name:      typeSpec.Name.Name,
		Receiver:  "",
		Signature: signature,
		Doc:       doc,
		Code:      strings.TrimSuffix(buf.String(), "\n"),
	}
}