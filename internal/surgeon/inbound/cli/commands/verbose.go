package commands

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// printSymbols prints a detailed report of generated symbols to stdout.
// Each symbol shows its name, receiver, file location, and full code.
func printSymbols(label string, results []domain.SymbolResult) {
  fmt.Printf("Generated %d %s:\n\n", len(results), label)
  for _, r := range results {
    bodyLines := r.LineEnd - r.LineStart + 1
    if r.Receiver != "" {
      fmt.Printf("  %s.%s  %s:%d-%d (%d lines)\n", r.Receiver, r.Name, r.File, r.LineStart, r.LineEnd, bodyLines)
    } else {
      fmt.Printf("  %s  %s:%d-%d (%d lines)\n", r.Name, r.File, r.LineStart, r.LineEnd, bodyLines)
    }
    fmt.Printf("  %s\n\n", r.Signature)
  }
}

// parseFileSymbols reads a Go file and returns SymbolResults for all exported symbols.
func parseFileSymbols(filePath string) []domain.SymbolResult {
  src, err := os.ReadFile(filePath)
  if err != nil {
    return nil
  }
  fset := token.NewFileSet()
  f, err := parser.ParseFile(fset, filePath, src, parser.ParseComments)
  if err != nil {
    return nil
  }
  var results []domain.SymbolResult
  for _, decl := range f.Decls {
    switch d := decl.(type) {
    case *ast.FuncDecl:
      if !d.Name.IsExported() {
        continue
      }
      recv := ""
      if d.Recv != nil && len(d.Recv.List) > 0 {
        switch t := d.Recv.List[0].Type.(type) {
        case *ast.StarExpr:
          if id, ok := t.X.(*ast.Ident); ok {
            recv = "*" + id.Name
          }
        case *ast.Ident:
          recv = t.Name
        }
      }
      startPos := fset.Position(d.Pos())
      endPos := fset.Position(d.End())
      sigEnd := d.Body.Pos()
      sigBytes := src[startPos.Offset : fset.Position(sigEnd).Offset]
      results = append(results, domain.SymbolResult{
        File:      filePath,
        LineStart: startPos.Line,
        LineEnd:   endPos.Line,
        Name:      d.Name.Name,
        Receiver:  recv,
        Signature: strings.TrimSpace(string(sigBytes)),
      })
    case *ast.GenDecl:
      if d.Tok != token.TYPE {
        continue
      }
      for _, spec := range d.Specs {
        ts, ok := spec.(*ast.TypeSpec)
        if !ok || !ts.Name.IsExported() {
          continue
        }
        startPos := fset.Position(d.Pos())
        endPos := fset.Position(d.End())
        results = append(results, domain.SymbolResult{
          File:      filePath,
          LineStart: startPos.Line,
          LineEnd:   endPos.Line,
          Name:      ts.Name.Name,
        })
      }
    }
  }
  return results
}
