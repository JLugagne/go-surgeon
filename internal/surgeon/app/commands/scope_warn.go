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
	case *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.CaseClause:
		return "switch body"
	case *ast.SelectStmt, *ast.CommClause:
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

// liftTarget describes the result of searching for the outermost statement
// inside a function's body that still contains fileOff. It is used by insert
// operations (insert_before/insert_after) to auto-lift anchors matched inside
// a nested scope up to the enclosing top-level statement, so the insertion
// lands in the function body proper instead of deep inside a closure or
// conditional branch.
type liftTarget struct {
	// ShouldLift is true when fileOff is inside a nested scope (i.e., not
	// directly in fn.Body) and a top-level statement was identified.
	ShouldLift bool
	// TopStmt is the outermost *ast.Stmt within fn.Body.List whose extent
	// contains fileOff. Nil when ShouldLift is false.
	TopStmt ast.Stmt
	// InnerLabel is the human label of the innermost nested scope the
	// anchor landed in (e.g., "closure body", "if/else branch"). Empty
	// when no nested scope was entered.
	InnerLabel string
	// InnerLine is the 1-based file line number of the anchor position
	// (used for the lifted_from description).
	InnerLine int
	// TopLine is the 1-based file line number where the top-level
	// statement starts (used for the lifted_to description).
	TopLine int
}

// findLiftTarget searches for the outermost statement inside fn.Body.List
// whose extent covers fileOff. It returns the "innermost scope-introducing
// node" along the path, used to describe where the anchor landed.
//
// If fileOff is already directly inside fn.Body (not inside any nested scope
// such as a closure, if-branch, for-loop, switch-case, etc.), ShouldLift is
// false and TopStmt is nil.
//
// Never lifts across a function boundary: nested *ast.FuncDecl (not possible
// in Go, but safe-guarded) and top-level func declarations are out of scope
// because we only walk fn.Body.
func findLiftTarget(fset *token.FileSet, fn *ast.FuncDecl, fileOff int) liftTarget {
	var lt liftTarget
	if fn == nil || fn.Body == nil || fset == nil {
		return lt
	}
	bodyStart := fset.Position(fn.Body.Lbrace).Offset
	bodyEnd := fset.Position(fn.Body.Rbrace).Offset
	if fileOff <= bodyStart || fileOff >= bodyEnd {
		return lt
	}

	// Find the outermost statement in fn.Body.List that contains fileOff.
	var topStmt ast.Stmt
	for _, stmt := range fn.Body.List {
		ns := fset.Position(stmt.Pos()).Offset
		ne := fset.Position(stmt.End()).Offset
		if fileOff >= ns && fileOff < ne {
			topStmt = stmt
			break
		}
	}
	if topStmt == nil {
		return lt
	}

	// Decide whether the anchor is nested inside a scope within topStmt.
	// An anchor is "nested" if any scope-introducing node on the AST path
	// from topStmt down to the anchor has an extent that strictly
	// encloses fileOff. We seed innerLabel with topStmt's own label so
	// statements that ARE scopes (for, if, switch, select) count — e.g.
	// an anchor inside a for-loop body should lift to the for-statement.
	innerLabel := innerScopeLabel(topStmt)
	ast.Inspect(topStmt, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		if n == topStmt {
			return true
		}
		nodeStart := fset.Position(n.Pos()).Offset
		nodeEnd := fset.Position(n.End()).Offset
		if fileOff < nodeStart || fileOff >= nodeEnd {
			return false
		}
		if label := innerScopeLabel(n); label != "" {
			innerLabel = label
		}
		return true
	})
	if innerLabel == "" {
		// The anchor is inside topStmt but not in a nested scope. For
		// simple statements (assignments, expressions) this means the
		// anchor IS the top-level statement — no lift needed.
		return lt
	}
	// Edge case: if topStmt is itself a scope-introducer but the anchor
	// lines up with the KEYWORD line (e.g. `for` / `if`) — meaning
	// fileOff is on the same line as topStmt.Pos() — the caller clearly
	// targeted the top-level statement itself, so don't lift.
	topKwLine := fset.Position(topStmt.Pos()).Line
	hitLine := 0
	if file := fset.File(fn.Pos()); file != nil {
		hitLine = file.Position(file.Pos(fileOff)).Line
	}
	if hitLine == topKwLine && innerScopeLabel(topStmt) != "" {
		return lt
	}

	lt.ShouldLift = true
	lt.TopStmt = topStmt
	lt.InnerLabel = innerLabel
	if file := fset.File(fn.Pos()); file != nil {
		lt.InnerLine = file.Position(file.Pos(fileOff)).Line
	}
	lt.TopLine = fset.Position(topStmt.Pos()).Line
	return lt
}
