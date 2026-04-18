package commands

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// liftedInsertPlan describes how an insert_before/insert_after operation
// should be applied after the anchor has been auto-lifted (or not) out of a
// nested scope.
type liftedInsertPlan struct {
	// Start, End are body-relative byte offsets of the edit (both equal
	// for a pure insert).
	Start, End int
	// Line is the text to splice in, including leading indent and
	// trailing newline.
	Line string
	// LineCount is the number of lines in Line (always >=1).
	LineCount int
	// Lift is populated when the anchor was auto-lifted. Nil when the
	// anchor was already at the top level.
	Lift *domain.AutoLiftInfo
}

// resolveInsertAnchor computes the lifted edit plan for an insert_before or
// insert_after patch. hitOffset is the body-relative start offset of the
// matched anchor.
//
// When the anchor lands inside a nested scope (closure, if-branch, loop,
// switch case), the insertion is auto-lifted so that it occurs immediately
// before (or after) the outermost enclosing statement in the target function
// body. The Lift field on the returned plan records the move, and the caller
// is expected to populate Lift.Context with the context lines after the file
// has been assembled.
func resolveInsertAnchor(
	fset *token.FileSet,
	fn *ast.FuncDecl,
	origBody string,
	bodyFileStart int,
	bodyStartLine int,
	patchIdx int,
	op domain.PatchOp,
	code string,
	hitOffset int,
) liftedInsertPlan {
	hitFileOff := bodyFileStart + hitOffset
	lt := findLiftTarget(fset, fn, hitFileOff)

	codeTrim := strings.TrimSpace(code)

	if !lt.ShouldLift {
		// Anchor is already at the function's top level.
		indent := lineIndent(origBody, hitOffset)
		line := indent + codeTrim + "\n"
		if op == domain.PatchOpInsertBefore {
			ls := lineStartOffset(origBody, hitOffset)
			return liftedInsertPlan{Start: ls, End: ls, Line: line, LineCount: 1}
		}
		le := lineEndOffset(origBody, hitOffset)
		return liftedInsertPlan{Start: le, End: le, Line: line, LineCount: 1}
	}

	// Lift: translate top-level statement bounds into body-relative coords
	// and anchor at its first or last line.
	stmtStartFile := fset.Position(lt.TopStmt.Pos()).Offset
	stmtEndFile := fset.Position(lt.TopStmt.End()).Offset
	bs := stmtStartFile - bodyFileStart
	be := stmtEndFile - bodyFileStart
	if bs < 0 {
		bs = 0
	}
	if be > len(origBody) {
		be = len(origBody)
	}
	indent := lineIndent(origBody, bs)
	line := indent + codeTrim + "\n"

	var start, end int
	var anchorLineInFile int
	if op == domain.PatchOpInsertBefore {
		start = lineStartOffset(origBody, bs)
		end = start
		anchorLineInFile = lt.TopLine
	} else {
		start = lineEndOffset(origBody, be-1)
		end = start
		// Final line of the top-level statement.
		anchorLineInFile = fset.Position(lt.TopStmt.End()).Line
	}

	innerLine := lt.InnerLine
	if innerLine == 0 {
		innerLine = bodyStartLine + strings.Count(origBody[:hitOffset], "\n")
	}

	info := &domain.AutoLiftInfo{
		PatchIndex: patchIdx,
		LiftedFrom: fmt.Sprintf("%s at L%d", lt.InnerLabel, innerLine),
		LiftedTo:   fmt.Sprintf("function body at L%d", anchorLineInFile),
	}
	return liftedInsertPlan{Start: start, End: end, Line: line, LineCount: 1, Lift: info}
}

// contextAroundInsertion returns a ±radius-non-blank-line window around the
// inserted lines, using the "line_number: content" format from
// symbol body=true, with '+' markers on inserted lines.
//
// afterSrc is the file content AFTER the insert has been applied.
// insertLineStart is the 1-based file line number of the first inserted line.
// insertedLineCount is how many lines were inserted (usually 1).
func contextAroundInsertion(afterSrc []byte, insertLineStart, insertedLineCount, radius int) string {
	lines := strings.Split(string(afterSrc), "\n")
	if insertLineStart < 1 || insertLineStart > len(lines) {
		return ""
	}
	idx0 := insertLineStart - 1

	startIdx := idx0
	nonBlank := 0
	for startIdx > 0 && nonBlank < radius {
		startIdx--
		if strings.TrimSpace(lines[startIdx]) != "" {
			nonBlank++
		}
	}
	endIdx := idx0 + insertedLineCount - 1
	if endIdx >= len(lines) {
		endIdx = len(lines) - 1
	}
	nonBlank = 0
	for endIdx < len(lines)-1 && nonBlank < radius {
		endIdx++
		if strings.TrimSpace(lines[endIdx]) != "" {
			nonBlank++
		}
	}

	var b strings.Builder
	for i := startIdx; i <= endIdx; i++ {
		marker := " "
		if i >= idx0 && i < idx0+insertedLineCount {
			marker = "+"
		}
		fmt.Fprintf(&b, "%s %d: %s\n", marker, i+1, lines[i])
	}
	return strings.TrimRight(b.String(), "\n")
}

// liftsAgree returns true when every hit in hits maps to the SAME top-level
// statement in fn.Body (either because they all lie in the same nested scope,
// or because the same top-level statement wraps each). In that case, the
// auto-lift position is unambiguous and we can proceed even without an
// explicit occurrence disambiguation.
//
// Hits that are already at the top level count as their own statement: if
// any hit is top-level and another is nested, they cannot agree.
func liftsAgree(fset *token.FileSet, fn *ast.FuncDecl, bodyFileStart int, hits [][2]int) bool {
	if len(hits) == 0 {
		return false
	}
	var shared token.Pos
	for i, h := range hits {
		lt := findLiftTarget(fset, fn, bodyFileStart+h[0])
		var pos token.Pos
		if lt.ShouldLift && lt.TopStmt != nil {
			pos = lt.TopStmt.Pos()
		} else {
			// Top-level hit: identify by its own position.
			pos = token.Pos(bodyFileStart + h[0])
		}
		if i == 0 {
			shared = pos
			continue
		}
		if pos != shared {
			return false
		}
	}
	return true
}

// ambiguousLiftCandidates groups hits by their auto-lift target so callers
// can surface candidate top-level statements when several nested hits would
// lift to DIFFERENT outer statements — that's the "lifted position is also
// ambiguous" case from the design spec.
//
// Returns a slice of "Lstart-Lend: <first line trimmed>" strings, one per
// distinct top-level statement that is a candidate lift target.
func ambiguousLiftCandidates(fset *token.FileSet, fn *ast.FuncDecl, src []byte, bodyFileStart int, hits [][2]int) []string {
	seen := make(map[token.Pos]bool)
	var out []string
	for _, h := range hits {
		fileOff := bodyFileStart + h[0]
		lt := findLiftTarget(fset, fn, fileOff)
		if !lt.ShouldLift {
			// Top-level hit — also a candidate: describe by its line.
			var line int
			if f := fset.File(fn.Pos()); f != nil {
				line = f.Position(f.Pos(fileOff)).Line
			}
			key := token.Pos(-line) // negative to avoid collision with TopStmt keys
			if !seen[key] {
				seen[key] = true
				out = append(out, fmt.Sprintf("L%d (top-level)", line))
			}
			continue
		}
		if seen[lt.TopStmt.Pos()] {
			continue
		}
		seen[lt.TopStmt.Pos()] = true
		startPos := fset.Position(lt.TopStmt.Pos())
		endPos := fset.Position(lt.TopStmt.End())
		firstLine := extractFileLine(src, startPos.Offset)
		out = append(out, fmt.Sprintf("L%d-L%d: %s", startPos.Line, endPos.Line, strings.TrimSpace(firstLine)))
	}
	return out
}

// extractFileLine returns the text of the line containing off in src.
func extractFileLine(src []byte, off int) string {
	if off < 0 || off >= len(src) {
		return ""
	}
	start := off
	for start > 0 && src[start-1] != '\n' {
		start--
	}
	end := off
	for end < len(src) && src[end] != '\n' {
		end++
	}
	return string(src[start:end])
}
