package commands

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/pmezard/go-difflib/difflib"
)

// Safety limits for regex matching.
const (
	// maxRegexPatternLen caps the source length of a match_regex pattern. Patterns
	// longer than this are rejected before we ever call regexp.Compile, which
	// prevents expensive compilation of pathological inputs.
	maxRegexPatternLen = 1024

	// maxRegexMatches caps the number of regex hits we return for a single patch.
	// Past this we refuse the patch and ask the user to narrow the pattern. This
	// bounds memory and error-path string formatting on patterns that match
	// explosively (e.g. `.` on a large body).
	maxRegexMatches = 1000

	// maxRegexCompileTime caps how long regex compilation may take. Go's regexp
	// is RE2-based and cannot catastrophically backtrack at match time, but huge
	// repetition counts (e.g. a{1000000}) can still be slow/allocation-heavy to
	// compile.
	maxRegexCompileTime = 200 * time.Millisecond

	// maxCandidatesShown caps how many "did you mean?" candidate lines we list
	// in the error message for an ambiguous match. Without this, a pattern that
	// matches 10000 times would produce a 10000-line error response.
	maxCandidatesShown = 20
)

// PatchFunction applies one or more scoped patches to the body of a named
// function or method. All patches are resolved against the original body
// (offset-stable), then applied atomically — if any resolution fails, nothing
// is written.
func (h *ExecutePlanHandler) PatchFunction(ctx context.Context, req domain.PatchFunctionRequest) (domain.PatchFunctionResult, error) {
	src, err := h.fs.ReadFile(ctx, req.FilePath)
	if err != nil {
		return domain.PatchFunctionResult{}, &domain.Error{Code: "READ_ERROR", Message: "failed to read file", Err: err}
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, req.FilePath, src, parser.ParseComments)
	if err != nil {
		return domain.PatchFunctionResult{}, &domain.Error{Code: "PARSE_ERROR", Message: "failed to parse file", Err: err}
	}

	// Locate the target function.
	recvTarget, nameTarget := parseIdentifier(req.Identifier)
	var targetFn *ast.FuncDecl
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != nameTarget {
			continue
		}
		var recvName string
		if fn.Recv != nil {
			recvName = getRecvType(fn.Recv)
		}
		if recvName == recvTarget || (recvName == "" && recvTarget == f.Name.Name) {
			targetFn = fn
			break
		}
	}
	if targetFn == nil {
		return domain.PatchFunctionResult{}, &domain.Error{
			Code:    "NODE_NOT_FOUND",
			Message: fmt.Sprintf("function %q not found in %s", req.Identifier, req.FilePath),
		}
	}
	if targetFn.Body == nil {
		return domain.PatchFunctionResult{}, &domain.Error{
			Code:    "NODE_NOT_FOUND",
			Message: fmt.Sprintf("function %q has no body", req.Identifier),
		}
	}

	lbraceOff := fset.Position(targetFn.Body.Lbrace).Offset // offset of '{'
	rbraceOff := fset.Position(targetFn.Body.Rbrace).Offset // offset of '}'
	origBody := string(src[lbraceOff+1 : rbraceOff])

	// Phase 1: resolve all patches against the original body.
	type resolvedEdit struct {
		start, end  int    // byte offsets relative to origBody start
		replacement string // text to substitute
	}
	edits := make([]resolvedEdit, len(req.Patches))
	var errs []string

	for i, p := range req.Patches {
		if p.Match == "" && p.MatchRegex == "" {
			errs = append(errs, fmt.Sprintf("patch #%d (%s): match or match_regex is required", i+1, p.Op))
			continue
		}
		if p.Match != "" && p.MatchRegex != "" {
			errs = append(errs, fmt.Sprintf("patch #%d (%s): match and match_regex are mutually exclusive", i+1, p.Op))
			continue
		}

		var hits [][2]int // [start, end] byte ranges in origBody
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
			errs = append(errs, fmt.Sprintf("patch #%d (%s %q): no match found in body of %s", i+1, p.Op, p.Match+p.MatchRegex, req.Identifier))
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
				"patch #%d (%s %q): matched %d times in body of %s. Disambiguate with occurrence: 1..%d. Candidates:\n%s%s",
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
		}
		hit := hits[idx]

		switch p.Op {
		case domain.PatchOpReplace:
			edits[i] = resolvedEdit{start: hit[0], end: hit[1], replacement: p.Replace}

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
			// Use the trimmed matched text as the %s argument so the agent does
			// not need to reproduce indentation in the wrap template.
			// The result is re-indented to match the original line's indentation.
			indent := lineIndent(origBody, hit[0])
			trimmedMatch := strings.TrimSpace(origBody[hit[0]:hit[1]])
			replacement := indent + fmt.Sprintf(p.Wrap, trimmedMatch)
			if wrapErr := validateGoStmt(replacement); wrapErr != nil {
				errs = append(errs, fmt.Sprintf("patch #%d (wrap): result of wrap does not parse as a Go statement: %v", i+1, wrapErr))
				continue
			}
			// Replace the whole line (so indentation is not duplicated).
			lineStart := lineStartOffset(origBody, hit[0])
			lineEnd := lineEndOffset(origBody, hit[0])
			edits[i] = resolvedEdit{start: lineStart, end: lineEnd, replacement: replacement + "\n"}

		default:
			errs = append(errs, fmt.Sprintf("patch #%d: unknown op %q (must be replace, insert_before, insert_after, delete, wrap)", i+1, p.Op))
		}
	}

	if len(errs) > 0 {
		return domain.PatchFunctionResult{}, &domain.Error{
			Code:    "PATCH_FAILED",
			Message: strings.Join(errs, "\n"),
		}
	}

	// Phase 2: apply edits to origBody working backwards (highest offset first).
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

	// Reassemble the full file.
	newSrc := make([]byte, 0, len(src)+len(newBody)-len(origBody))
	newSrc = append(newSrc, src[:lbraceOff+1]...)
	newSrc = append(newSrc, newBody...)
	newSrc = append(newSrc, src[rbraceOff:]...)

	diff := diffStrings(req.FilePath, string(src), string(newSrc))

	if req.Preview {
		return domain.PatchFunctionResult{Diff: diff, Applied: len(req.Patches), Preview: true}, nil
	}

	if err := h.fs.WriteFile(ctx, req.FilePath, newSrc); err != nil {
		return domain.PatchFunctionResult{}, &domain.Error{Code: "WRITE_ERROR", Message: "failed to write file", Err: err}
	}
	_ = h.fs.ExecuteGoImports(ctx, []string{req.FilePath})

	return domain.PatchFunctionResult{Diff: diff, Applied: len(req.Patches)}, nil
}

// safeRegexMatches compiles pattern and returns all non-empty match byte ranges
// in body, with safety guards:
//   - pattern length capped (maxRegexPatternLen)
//   - pattern must be valid UTF-8 (regexp.Compile already enforces this, but we
//     reject early for a clearer error)
//   - compilation is bounded by maxRegexCompileTime on a separate goroutine;
//     pathological patterns like a{99999999} would otherwise spin the parser
//   - match count capped (maxRegexMatches); explosive patterns ('.', '\B',
//     '(?:)') produce an actionable error instead of a million-entry slice
//   - zero-width matches are rejected — patch ops need a non-empty byte range
//
// Go's regexp is RE2-based (linear time, no backreferences, no lookarounds), so
// we do not need to protect against catastrophic backtracking the way a PCRE
// wrapper would.
func safeRegexMatches(pattern, body string) ([][2]int, error) {
	if len(pattern) > maxRegexPatternLen {
		return nil, fmt.Errorf("match_regex too long: %d bytes (max %d) — narrow the pattern",
			len(pattern), maxRegexPatternLen)
	}
	if !utf8.ValidString(pattern) {
		return nil, fmt.Errorf("match_regex is not valid UTF-8")
	}

	// Compile with a timeout. regexp.Compile is CPU-bound and cannot be
	// cancelled mid-flight, but we can at least reject the result if it took
	// too long, which prevents a slow patch from blocking the whole request.
	type compileResult struct {
		re  *regexp.Regexp
		err error
	}
	done := make(chan compileResult, 1)
	go func() {
		re, err := regexp.Compile(pattern)
		done <- compileResult{re: re, err: err}
	}()
	var re *regexp.Regexp
	select {
	case r := <-done:
		if r.err != nil {
			return nil, fmt.Errorf("invalid match_regex: %w", r.err)
		}
		re = r.re
	case <-time.After(maxRegexCompileTime):
		return nil, fmt.Errorf("match_regex took too long to compile (>%s) — simplify the pattern",
			maxRegexCompileTime)
	}

	// Cap matches at maxRegexMatches+1 so we can tell "over limit" apart from
	// "exactly at limit".
	raw := re.FindAllStringIndex(body, maxRegexMatches+1)
	if len(raw) > maxRegexMatches {
		return nil, fmt.Errorf("match_regex matched more than %d times — narrow the pattern",
			maxRegexMatches)
	}

	hits := make([][2]int, 0, len(raw))
	for _, m := range raw {
		if m[0] == m[1] {
			// Zero-width matches (e.g. from '^', '$', '(?:)') would delete or
			// insert nothing — refuse them so the user gets a clear error
			// instead of a no-op that is ambiguous with "no match found".
			return nil, fmt.Errorf("match_regex produced a zero-width match — pattern must match at least one character")
		}
		hits = append(hits, [2]int{m[0], m[1]})
	}
	return hits, nil
}

// findNormalizedMatches returns all [start,end] byte ranges in body where
// the whitespace-normalized content matches the normalized match string.
// Matching is attempted at three granularities:
//  1. whole-line  (match trims to the entire trimmed line)
//  2. sub-string  (match found anywhere within a line after normalizing spaces)
//  3. multi-line  (match spans multiple lines after collapsing all whitespace runs)
func findNormalizedMatches(body, match string) [][2]int {
	normMatch := normalizeWS(match)
	var hits [][2]int

	lines := strings.Split(body, "\n")
	// Track byte offset of the start of each line.
	offsets := make([]int, len(lines))
	pos := 0
	for i, l := range lines {
		offsets[i] = pos
		pos += len(l) + 1 // +1 for the '\n'
	}

	// 1 & 2: single-line scan.
	for i, l := range lines {
		normLine := normalizeWS(l)
		if normLine == normMatch {
			// whole-line match — span the full line content (not the newline)
			hits = append(hits, [2]int{offsets[i], offsets[i] + len(l)})
			continue
		}
		// sub-string match using normalised versions — find position in original.
		if idx := strings.Index(normLine, normMatch); idx != -1 {
			// Map the normalized index back to the original line via rune scan.
			start, end := mapNormIndexToOrig(l, normMatch)
			if start >= 0 {
				hits = append(hits, [2]int{offsets[i] + start, offsets[i] + end})
			}
		}
	}
	if len(hits) > 0 {
		return hits
	}

	// 3: multi-line: collapse all body whitespace and try to find the match.
	normBody := normalizeWS(body)
	searchFrom := 0
	for {
		idx := strings.Index(normBody[searchFrom:], normMatch)
		if idx < 0 {
			break
		}
		absIdx := searchFrom + idx
		start, end := mapNormBodyToOrig(body, normBody, absIdx, absIdx+len(normMatch))
		if start >= 0 {
			hits = append(hits, [2]int{start, end})
		}
		searchFrom = absIdx + 1
	}
	return hits
}

// normalizeWS collapses all runs of whitespace (including tabs and newlines)
// to a single space and trims leading/trailing whitespace.
func normalizeWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// mapNormIndexToOrig finds the byte range in orig that corresponds to the
// normalised needle. Returns (-1,-1) on failure.
func mapNormIndexToOrig(orig, normNeedle string) (int, int) {
	fields := strings.Fields(normNeedle)
	if len(fields) == 0 {
		return -1, -1
	}
	// Walk orig looking for the first field.
	first := fields[0]
	i := 0
	for i < len(orig) {
		idx := strings.Index(orig[i:], first)
		if idx < 0 {
			return -1, -1
		}
		start := i + idx
		// Try to match all remaining fields from here.
		pos := start + len(first)
		matched := true
		for _, f := range fields[1:] {
			// Skip whitespace.
			for pos < len(orig) && (orig[pos] == ' ' || orig[pos] == '\t') {
				pos++
			}
			if !strings.HasPrefix(orig[pos:], f) {
				matched = false
				break
			}
			pos += len(f)
		}
		if matched {
			return start, pos
		}
		i = start + 1
	}
	return -1, -1
}

// mapNormBodyToOrig maps a byte range in the normalised body back to the original body.
func mapNormBodyToOrig(orig, normOrig string, normStart, normEnd int) (int, int) {
	// Walk both strings simultaneously, tracking correspondence.
	oi, ni := 0, 0
	origStart := -1
	for ni < len(normOrig) && oi < len(orig) {
		if ni == normStart {
			origStart = oi
		}
		if ni == normEnd {
			return origStart, oi
		}
		nc := normOrig[ni]
		oc := orig[oi]
		if nc == oc {
			ni++
			oi++
		} else if nc == ' ' {
			// The normalized string has a space; orig may have multiple ws chars.
			ni++
			for oi < len(orig) && (orig[oi] == ' ' || orig[oi] == '\t' || orig[oi] == '\n' || orig[oi] == '\r') {
				oi++
			}
		} else {
			return -1, -1
		}
	}
	if ni == normEnd && origStart >= 0 {
		return origStart, oi
	}
	return -1, -1
}

// lineIndent returns the leading whitespace of the line that contains offset off in body.
func lineIndent(body string, off int) string {
	start := lineStartOffset(body, off)
	line := body[start:]
	trimmed := strings.TrimLeft(line, " \t")
	return line[:len(line)-len(trimmed)]
}

// lineStartOffset returns the offset of the start of the line containing off.
func lineStartOffset(body string, off int) int {
	for i := off - 1; i >= 0; i-- {
		if body[i] == '\n' {
			return i + 1
		}
	}
	return 0
}

// lineEndOffset returns the offset just after the '\n' that ends the line containing off.
func lineEndOffset(body string, off int) int {
	for i := off; i < len(body); i++ {
		if body[i] == '\n' {
			return i + 1
		}
	}
	return len(body)
}

// deletionRange expands [start,end] to include the whole line and its newline
// when the matched fragment is the entire non-whitespace content of that line.
func deletionRange(body string, start, end int) (int, int) {
	lineStart := lineStartOffset(body, start)
	lineEnd := lineEndOffset(body, end-1)
	lineContent := body[lineStart:lineEnd]
	if strings.TrimSpace(lineContent) == strings.TrimSpace(body[start:end]) {
		return lineStart, lineEnd
	}
	return start, end
}

// validateGoStmt checks that s parses as a valid Go statement inside a dummy function.
func validateGoStmt(s string) error {
	src := "package p\nfunc _(){\n" + s + "\n}\n"
	_, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
	return err
}

// extractLine returns the full line from body that contains byte offset off.
func extractLine(body string, off int) string {
	start := lineStartOffset(body, off)
	end := lineEndOffset(body, off)
	return body[start:end]
}

// diffStrings produces a unified diff between two file versions.
func diffStrings(filename, oldSrc, newSrc string) string {
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(oldSrc),
		B:        difflib.SplitLines(newSrc),
		FromFile: filename + " (original)",
		ToFile:   filename + " (patched)",
		Context:  3,
	}
	text, _ := difflib.GetUnifiedDiffString(diff)
	return text
}
