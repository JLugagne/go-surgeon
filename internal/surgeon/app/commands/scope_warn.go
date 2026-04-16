package commands

import (
	"fmt"
	"go/ast"
	"go/token"
)

// describeInnerScope returns a short human label for a scope-statement kind
// nested inside a function body — "for-loop body", "if/else branch", etc.
// Returns "" when the file offset sits directly in the function body without
// crossing into a nested block.
//
// fileOff is a 0-based byte offset into the original source file (the same
// kind that fset.Position(...).Offset returns).
func describeInnerScope(fset *token.FileSet, fn *ast.FuncDecl, fileOff int) string {
	if fn == nil || fn.Body == nil || fset == nil {
		return ""
	}
	bodyStart := fset.Position(fn.Body.Lbrace).Offset
	bodyEnd := fset.Position(fn.Body.Rbrace).Offset
	if fileOff <= bodyStart || fileOff >= bodyEnd {
		return ""
	}
	var innermost string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		if n == fn.Body {
			return true
		}
		nodeStart := fset.Position(n.Pos()).Offset
		nodeEnd := fset.Position(n.End()).Offset
		if fileOff < nodeStart || fileOff >= nodeEnd {
			return false
		}
		if label := innerScopeLabel(n); label != "" {
			innermost = label
		}
		return true
	})
	return innermost
}

// innerScopeLabel returns a human-readable label for AST nodes that introduce
// a new nested scope inside a function body. Returns "" for nodes that are
// not scope-introducing.
func innerScopeLabel(n ast.Node) string {
	switch n.(type) {
	case *ast.ForStmt, *ast.RangeStmt:
		return "for-loop body"
	case *ast.IfStmt:
		return "if/else branch"
	case *ast.SwitchStmt, *ast.TypeSwitchStmt:
		return "switch body"
	case *ast.SelectStmt:
		return "select body"
	case *ast.FuncLit:
		return "closure body"
	}
	return ""
}

// innerScopeWarning composes a warning for an insert operation that landed
// inside an inner scope. Returns "" when label is empty.
func innerScopeWarning(patchIdx int, label string, lineInFile int) string {
	if label == "" {
		return ""
	}
	return fmt.Sprintf(
		"patch #%d: insert lands inside %s at L%d. If you intended to insert outside it, anchor on a statement outside the block or use at_line with a line outside the block.",
		patchIdx, label, lineInFile,
	)
}
