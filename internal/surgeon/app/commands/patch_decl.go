package commands

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// PatchDecl applies one or more scoped patches to the VALUE expression of a
// top-level const or var declaration. All patches are resolved against the
// original value text (offset-stable), then applied atomically — if any
// resolution fails, nothing is written.
//
// Body-extraction semantics (see ADR 0006):
//   - Single string BasicLit value: origBody is the content INSIDE the
//     quotes/backticks (delimiters are preserved automatically).
//   - Any other value expression: origBody is the full value-expression text
//     exactly as it appears in source.
//
// Typed vars without an initializer (`var x int`) are rejected with
// NODE_NOT_FOUND — there is no value to patch.
func (h *ExecutePlanHandler) PatchDecl(ctx context.Context, req domain.PatchDeclRequest) (domain.PatchDeclResult, error) {
	src, err := h.fs.ReadFile(ctx, req.FilePath)
	if err != nil {
		return domain.PatchDeclResult{}, &domain.Error{Code: "READ_ERROR", Message: "failed to read file", Err: err}
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, req.FilePath, src, parser.ParseComments)
	if err != nil {
		return domain.PatchDeclResult{}, &domain.Error{Code: "PARSE_ERROR", Message: "failed to parse file", Err: err}
	}

	spec, valueExpr, ok := findDeclValueSpec(f, req.Identifier)
	if !ok {
		return domain.PatchDeclResult{}, &domain.Error{
			Code:    "NODE_NOT_FOUND",
			Message: fmt.Sprintf("top-level const or var %q not found in %s", req.Identifier, req.FilePath),
		}
	}
	if valueExpr == nil {
		return domain.PatchDeclResult{}, &domain.Error{
			Code:    "NODE_NOT_FOUND",
			Message: fmt.Sprintf("declaration %q has no value expression (typed var without initializer) — use update to add one", req.Identifier),
		}
	}

	bodyStartOff, bodyEndOff, bodyStartLine := declBodyOffsets(fset, src, valueExpr)
	origBody := string(src[bodyStartOff:bodyEndOff])

	var warnings []string
	// Phase 1: resolve all patches against the original body.
	type resolvedEdit struct {
		start, end  int
		replacement string
	}
	edits := make([]resolvedEdit, len(req.Patches))
	var errs []string

	for i, p := range req.Patches {
		lineMode := p.AtLine > 0 || p.FromLine > 0 || p.ToLine > 0
		if lineMode && (p.Match != "" || p.MatchRegex != "") {
			errs = append(errs, fmt.Sprintf("patch #%d (%s): line-based targets (at_line/from_line/to_line) are mutually exclusive with match/match_regex", i+1, p.Op))
			continue
		}
		if !lineMode && p.Match == "" && p.MatchRegex == "" {
			errs = append(errs, fmt.Sprintf("patch #%d (%s): match, match_regex, at_line, or from_line/to_line is required", i+1, p.Op))
			continue
		}
		if p.Match != "" && p.MatchRegex != "" {
			errs = append(errs, fmt.Sprintf("patch #%d (%s): match and match_regex are mutually exclusive", i+1, p.Op))
			continue
		}

		if lineMode {
			fromL := p.FromLine
			toL := p.ToLine
			if fromL == 0 && toL == 0 {
				fromL = p.AtLine
				toL = p.AtLine
			} else if fromL == 0 {
				fromL = toL
			} else if toL == 0 {
				toL = fromL
			}
			start, end, okL := resolveBodyLineRange(origBody, bodyStartLine, fromL, toL)
			if !okL {
				errs = append(errs, fmt.Sprintf("patch #%d (%s): line range %d-%d out of %s value (value lines %d-%d)", i+1, p.Op, fromL, toL, req.Identifier, bodyStartLine, bodyStartLine+strings.Count(origBody, "\n")))
				continue
			}
			ls, le, lrepl := buildLineModeEdit(p, origBody, start, end)
			edits[i] = resolvedEdit{start: ls, end: le, replacement: lrepl}
			continue
		}

		var hits [][2]int
		if p.MatchRegex != "" {
			h, regexErr := safeRegexMatches(p.MatchRegex, origBody)
			if regexErr != nil {
				errs = append(errs, fmt.Sprintf("patch #%d (%s): %v", i+1, p.Op, regexErr))
				continue
			}
			hits = h
		} else {
			hits = findNormalizedMatches(origBody, p.Match)
		}

		if len(hits) == 0 {
			needle := p.Match
			if needle == "" {
				needle = p.MatchRegex
			}
			msg := fmt.Sprintf("patch #%d (%s %q): no match found in value of %s",
				i+1, p.Op, needle, req.Identifier)
			if suggestions := suggestClosestLines(origBody, needle, 3); suggestions != "" {
				msg += "\n  Closest lines in value:\n" + suggestions
			}
			errs = append(errs, msg)
			continue
		}
		if len(hits) > 1 && p.Occurrence == 0 {
			var candidates []string
			shown := hits
			if len(shown) > maxCandidatesShown {
				shown = shown[:maxCandidatesShown]
			}
			for _, h := range shown {
				line := extractLine(origBody, h[0])
				candidates = append(candidates, fmt.Sprintf("  %s", strings.TrimSpace(line)))
			}
			trailer := ""
			if len(hits) > maxCandidatesShown {
				trailer = fmt.Sprintf("\n  ... (%d more)", len(hits)-maxCandidatesShown)
			}
			errs = append(errs, fmt.Sprintf(
				"patch #%d (%s %q): matched %d times in value of %s. Disambiguate with occurrence: 1..%d. Candidates:\n%s%s",
				i+1, p.Op, p.Match+p.MatchRegex, len(hits), req.Identifier, len(hits), strings.Join(candidates, "\n"), trailer,
			))
			continue
		}

		idx := 0
		if p.Occurrence > 0 {
			if p.Occurrence > len(hits) {
				errs = append(errs, fmt.Sprintf(
					"patch #%d (%s %q): occurrence %d requested but only %d match(es) found",
					i+1, p.Op, p.Match+p.MatchRegex, p.Occurrence, len(hits),
				))
				continue
			}
			idx = p.Occurrence - 1
			if p.Op == domain.PatchOpReplace && len(hits) > 1 {
				var leftoverLines []int
				for j, lh := range hits {
					if j == idx {
						continue
					}
					leftoverLines = append(leftoverLines, bodyStartLine+strings.Count(origBody[:lh[0]], "\n"))
				}
				if len(leftoverLines) > 0 {
					numStrs := make([]string, len(leftoverLines))
					for k, n := range leftoverLines {
						numStrs[k] = fmt.Sprintf("L%d", n)
					}
					warnings = append(warnings, fmt.Sprintf("patch #%d: replaced occurrence %d; %d more match(es) remain at %s", i+1, p.Occurrence, len(leftoverLines), strings.Join(numStrs, ", ")))
				}
			}
		}
		hit := hits[idx]

		switch p.Op {
		case domain.PatchOpReplace:
			repl := p.Replace
			if repl != "" && !startsWithWhitespace(repl) {
				if indent := lineIndent(origBody, hit[0]); indent != "" && hit[0] == lineStartOffset(origBody, hit[0]) {
					repl = reIndentReplacement(repl, indent)
				}
			}
			edits[i] = resolvedEdit{start: hit[0], end: hit[1], replacement: repl}

		case domain.PatchOpInsertBefore:
			indent := lineIndent(origBody, hit[0])
			line := indent + strings.TrimSpace(p.Code) + "\n"
			lineStart := lineStartOffset(origBody, hit[0])
			edits[i] = resolvedEdit{start: lineStart, end: lineStart, replacement: line}

		case domain.PatchOpInsertAfter:
			indent := lineIndent(origBody, hit[0])
			line := indent + strings.TrimSpace(p.Code) + "\n"
			lineEnd := lineEndOffset(origBody, hit[0])
			edits[i] = resolvedEdit{start: lineEnd, end: lineEnd, replacement: line}

		case domain.PatchOpDelete:
			start, end := deletionRange(origBody, hit[0], hit[1])
			edits[i] = resolvedEdit{start: start, end: end, replacement: ""}

		case domain.PatchOpWrap:
			indent := lineIndent(origBody, hit[0])
			trimmedMatch := strings.TrimSpace(origBody[hit[0]:hit[1]])
			replacement := indent + fmt.Sprintf(p.Wrap, trimmedMatch)
			// Note: we deliberately do NOT run validateGoStmt here. The wrapped
			// text is inside a const/var value, not a function body — the value
			// might be a partial expression fragment that isn't a valid Go
			// statement on its own. The final validateGoSource pass below
			// still rejects anything that breaks the file.
			lineStart := lineStartOffset(origBody, hit[0])
			lineEnd := lineEndOffset(origBody, hit[0])
			edits[i] = resolvedEdit{start: lineStart, end: lineEnd, replacement: replacement + "\n"}

		default:
			errs = append(errs, fmt.Sprintf("patch #%d: unknown op %q (must be replace, insert_before, insert_after, delete, wrap)", i+1, p.Op))
		}
	}

	if len(errs) > 0 {
		msg := strings.Join(errs, "\n")
		declStart := fset.Position(spec.Pos())
		declEnd := fset.Position(spec.End())
		if body := formatNumberedSource(src, declStart.Offset, declEnd.Offset, declStart.Line); body != "" {
			msg += fmt.Sprintf("\n\nCurrent value of %s (lines %d-%d):\n%s", req.Identifier, declStart.Line, declEnd.Line, body)
		}
		msg += "\nHint: retry with from_line/to_line targeting using the line numbers above."
		return domain.PatchDeclResult{}, &domain.Error{
			Code:    "PATCH_FAILED",
			Message: msg,
		}
	}

	// Phase 2: apply edits back-to-front.
	order := make([]int, len(edits))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		return edits[order[a]].start > edits[order[b]].start
	})

	newBody := []byte(origBody)
	for _, i := range order {
		e := edits[i]
		newBody = append(newBody[:e.start], append([]byte(e.replacement), newBody[e.end:]...)...)
	}

	newSrc := make([]byte, 0, len(src)+len(newBody)-len(origBody))
	newSrc = append(newSrc, src[:bodyStartOff]...)
	newSrc = append(newSrc, newBody...)
	newSrc = append(newSrc, src[bodyEndOff:]...)

	if err := validateGoSource(req.FilePath, newSrc); err != nil {
		return domain.PatchDeclResult{}, err
	}

	diff := diffStrings(req.FilePath, string(src), string(newSrc))

	if req.Preview {
		return domain.PatchDeclResult{Diff: diff, Applied: len(req.Patches), Preview: true, Warnings: warnings}, nil
	}

	addedImports, err := h.fs.WriteFile(ctx, req.FilePath, newSrc)
	if err != nil {
		return domain.PatchDeclResult{}, &domain.Error{Code: "WRITE_ERROR", Message: "failed to write file", Err: err}
	}

	return domain.PatchDeclResult{Diff: diff, Applied: len(req.Patches), AddedImports: addedImports, Warnings: warnings}, nil
}

// findDeclValueSpec locates a top-level const/var declaration by name and
// returns the ValueSpec, the specific value expression for the named
// identifier, and whether the declaration was found.
//
// If the declaration exists but has no initializer (`var x int`), the
// second return value is nil while ok is true — callers surface that as
// NODE_NOT_FOUND with a dedicated message.
func findDeclValueSpec(f *ast.File, name string) (*ast.ValueSpec, ast.Expr, bool) {
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		if gen.Tok != token.CONST && gen.Tok != token.VAR {
			continue
		}
		for _, sp := range gen.Specs {
			vs, ok := sp.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, id := range vs.Names {
				if id.Name != name {
					continue
				}
				// Found the identifier. Locate its value expression.
				if i < len(vs.Values) {
					return vs, vs.Values[i], true
				}
				// No initializer — declared type only.
				return vs, nil, true
			}
		}
	}
	return nil, nil, false
}

// declBodyOffsets returns the byte offsets [start, end) of the "body" to
// patch against, plus the file line of the first byte. The body is
// extracted according to ADR 0006:
//   - single string BasicLit: content between the delimiters
//   - any other expression: full value-expression text
func declBodyOffsets(fset *token.FileSet, src []byte, expr ast.Expr) (int, int, int) {
	start := fset.Position(expr.Pos()).Offset
	end := fset.Position(expr.End()).Offset

	if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		// Strip a single byte on each side (`"` or `` ` ``).
		// Both delimiters are single ASCII bytes in Go source.
		if end-start >= 2 {
			innerStart := start + 1
			innerEnd := end - 1
			return innerStart, innerEnd, fset.Position(expr.Pos()).Line
		}
	}
	return start, end, fset.Position(expr.Pos()).Line
}
