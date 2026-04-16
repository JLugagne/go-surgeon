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
	bodyStartLine := fset.Position(targetFn.Body.Lbrace).Line
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
			start, end, ok := resolveBodyLineRange(origBody, bodyStartLine, fromL, toL)
			if !ok {
				errs = append(errs, fmt.Sprintf("patch #%d (%s): line range %d-%d out of %s body (body lines %d-%d)", i+1, p.Op, fromL, toL, req.Identifier, bodyStartLine, bodyStartLine+strings.Count(origBody, "\n")))
				continue
			}
			ls, le, lrepl := buildLineModeEdit(p, origBody, start, end)
			edits[i] = resolvedEdit{start: ls, end: le, replacement: lrepl}
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
			needle := p.Match
			if needle == "" {
				needle = p.MatchRegex
			}
			msg := fmt.Sprintf("patch #%d (%s %q): no match found in body of %s",
				i+1, p.Op, needle, req.Identifier)
			if suggestions := suggestClosestLines(origBody, needle, 3); suggestions != "" {
				msg += "\n  Closest lines in body:\n" + suggestions
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
			repl := p.Replace
			// Re-apply the original line's indentation when the replacement has none,
			// so callers don't need to reproduce leading whitespace.
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

	// Reject the patch before writing if it would produce invalid Go.
	if err := validateGoSource(req.FilePath, newSrc); err != nil {
		return domain.PatchFunctionResult{}, err
	}

	diff := diffStrings(req.FilePath, string(src), string(newSrc))

	if req.Preview {
		return domain.PatchFunctionResult{Diff: diff, Applied: len(req.Patches), Preview: true}, nil
	}

	addedImports, err := h.fs.WriteFile(ctx, req.FilePath, newSrc)
	if err != nil {
		return domain.PatchFunctionResult{}, &domain.Error{Code: "WRITE_ERROR", Message: "failed to write file", Err: err}
	}

	return domain.PatchFunctionResult{Diff: diff, Applied: len(req.Patches), AddedImports: addedImports}, nil
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
//
// normalizeWS uses strings.Fields, which trims leading and trailing whitespace
// runs entirely. So `oi` must start at the first non-whitespace byte of orig,
// not at byte 0 — otherwise the parallel walk fails immediately on bodies that
// begin with a newline or tab (which is virtually every function body).
func mapNormBodyToOrig(orig, normOrig string, normStart, normEnd int) (int, int) {
	oi, ni := 0, 0
	origStart := -1

	// Skip leading whitespace in orig — normOrig has none.
	for oi < len(orig) && (orig[oi] == ' ' || orig[oi] == '\t' || orig[oi] == '\n' || orig[oi] == '\r') {
		oi++
	}

	for ni < len(normOrig) && oi < len(orig) {
		if ni == normStart {
			origStart = oi
		}
		if ni == normEnd {
			return origStart, oi
		}
		nc := normOrig[ni]
		oc := orig[oi]
		switch nc {
		case oc:
			ni++
			oi++
		case ' ':
			// The normalized string has a space; orig may have multiple ws chars.
			ni++
			for oi < len(orig) && (orig[oi] == ' ' || orig[oi] == '\t' || orig[oi] == '\n' || orig[oi] == '\r') {
				oi++
			}
		default:
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

// validateGoSource parses src as a full Go file and returns a structured error
// if the result would be syntactically broken. This runs before writing so the
// caller can reject the patch instead of leaving a broken file on disk.
//
// The error message includes the line/column of the first parse error plus a
// two-line snippet around that location — enough for an agent to fix the patch
// on the next turn without having to re-read the file.
func validateGoSource(path string, src []byte) error {
	_, err := parser.ParseFile(token.NewFileSet(), path, src, parser.ParseComments)
	if err == nil {
		return nil
	}

	// go/scanner.ErrorList wraps one or more Error entries with positions.
	// Extract the first position and show a snippet for better feedback.
	msg := err.Error()
	snippet := firstErrorSnippet(src, err)
	if snippet != "" {
		msg = msg + "\n" + snippet
	}
	return &domain.Error{
		Code:    "PATCH_PRODUCES_INVALID_GO",
		Message: "patch would produce syntactically invalid Go — rejected before writing:\n" + msg,
	}
}

// firstErrorSnippet returns a ±1 line context around the first parse error, or
// "" if no position can be extracted. We regex-extract the location from the
// canonical parser message ("file:line:col: msg") rather than depending on
// go/scanner internals — the format is stable and the dependency is trivial.
func firstErrorSnippet(src []byte, parseErr error) string {
	m := errLocRegexp.FindStringSubmatch(parseErr.Error())
	if len(m) < 3 {
		return ""
	}
	line := atoi(m[1])
	col := atoi(m[2])
	if line <= 0 {
		return ""
	}
	lines := strings.Split(string(src), "\n")
	if line > len(lines) {
		return ""
	}
	var b strings.Builder
	start := line - 2
	if start < 1 {
		start = 1
	}
	end := line + 1
	if end > len(lines) {
		end = len(lines)
	}
	for i := start; i <= end; i++ {
		fmt.Fprintf(&b, "  %4d | %s\n", i, lines[i-1])
		if i == line && col > 0 {
			fmt.Fprintf(&b, "       | %s^\n", strings.Repeat(" ", col-1))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// errLocRegexp matches trailing "line:col:" in a parser error message.
var errLocRegexp = regexpMustCompile(`:(\d+):(\d+):`)

func regexpMustCompile(p string) *regexp.Regexp { return regexp.MustCompile(p) }

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// extractLine returns the full line from body that contains byte offset off.
func extractLine(body string, off int) string {
	start := lineStartOffset(body, off)
	end := lineEndOffset(body, off)
	return body[start:end]
}

// suggestClosestLines returns up to 'top' lines from body that share the most
// significant tokens with needle, formatted for an error message. Returns ""
// when no line shares any token with the needle. The goal is to help an agent
// recover from a failed patch on the next turn without re-reading the file.
//
// We score by the count of shared alphanumeric tokens of length >= 2, ignoring
// punctuation. That keeps the signal high (shared identifiers and keywords
// dominate) and avoids noisy "matches" on common glue like ":", "{", etc.
func suggestClosestLines(body, needle string, top int) string {
	needleTokens := tokenize(needle)
	if len(needleTokens) == 0 {
		return ""
	}
	needleSet := make(map[string]struct{}, len(needleTokens))
	for _, t := range needleTokens {
		needleSet[t] = struct{}{}
	}

	type scored struct {
		line   string
		lineNo int // 1-based
		score  int
	}
	var ranked []scored

	// Split into original lines with their 1-based numbering.
	lineNo := 1
	for _, line := range strings.Split(body, "\n") {
		score := 0
		for _, t := range tokenize(line) {
			if _, ok := needleSet[t]; ok {
				score++
			}
		}
		if score > 0 {
			ranked = append(ranked, scored{line: line, lineNo: lineNo, score: score})
		}
		lineNo++
	}
	if len(ranked) == 0 {
		return ""
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		return ranked[i].score > ranked[j].score
	})
	if len(ranked) > top {
		ranked = ranked[:top]
	}

	var b strings.Builder
	for _, r := range ranked {
		trimmed := strings.TrimSpace(r.line)
		if len(trimmed) > 120 {
			trimmed = trimmed[:117] + "..."
		}
		fmt.Fprintf(&b, "    L%d (shares %d tokens): %s\n", r.lineNo, r.score, trimmed)
	}
	return strings.TrimRight(b.String(), "\n")
}

// tokenize extracts alphanumeric tokens of length >= 2 from s. Underscores are
// treated as part of identifiers; punctuation and whitespace are separators.
func tokenize(s string) []string {
	var out []string
	start := -1
	for i := 0; i <= len(s); i++ {
		var isWord bool
		if i < len(s) {
			c := s[i]
			isWord = (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') || c == '_'
		}
		if isWord && start < 0 {
			start = i
		} else if !isWord && start >= 0 {
			if i-start >= 2 {
				out = append(out, s[start:i])
			}
			start = -1
		}
	}
	return out
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

// startsWithWhitespace reports whether s begins with a space or tab.
func startsWithWhitespace(s string) bool {
	return len(s) > 0 && (s[0] == ' ' || s[0] == '\t')
}

// reIndentReplacement prepends indent to the first line of repl and applies
// the same indent to any subsequent lines that have no leading whitespace.
// Lines that already carry their own indentation are left untouched.
func reIndentReplacement(repl, indent string) string {
	lines := strings.Split(repl, "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		if !startsWithWhitespace(l) {
			lines[i] = indent + l
		}
	}
	return strings.Join(lines, "\n")
}

// resolveBodyLineRange maps a file-absolute line range onto byte offsets
// within origBody. bodyStartLine is the file line of the opening '{' of
// the function. fromLine and toLine are inclusive and 1-based in the
// file's numbering (the same numbers symbol body=true prints).
//
// Returns (startOffset, endOffset, ok). When ok is false, at least one
// requested line falls outside the body.
// resolveBodyLineRange maps a file-absolute line range onto byte offsets
// within origBody. bodyStartLine is the file line of the opening '{' of
// the function. fromLine and toLine are inclusive and 1-based in the
// file's numbering (the same numbers symbol body=true prints).
//
// Returns (startOffset, endOffset, ok). When ok is false, at least one
// requested line falls outside the body.
func resolveBodyLineRange(origBody string, bodyStartLine, fromLine, toLine int) (int, int, bool) {
	if fromLine <= 0 || toLine < fromLine {
		return 0, 0, false
	}
	// origBody starts immediately after '{'. Line of offset 0 is bodyStartLine.
	// Advance through the body tracking where each line starts.
	line := bodyStartLine
	startOff := -1
	endOff := -1
	lineStart := 0
	for i := 0; i <= len(origBody); i++ {
		if line == fromLine && startOff < 0 {
			startOff = lineStart
		}
		if line == toLine+1 && endOff < 0 {
			endOff = lineStart
		}
		if i == len(origBody) {
			break
		}
		if origBody[i] == '\n' {
			line++
			lineStart = i + 1
		}
	}
	if startOff < 0 {
		return 0, 0, false
	}
	if endOff < 0 {
		endOff = len(origBody)
	}
	return startOff, endOff, true
}

// buildLineModeEdit converts a FunctionPatch with line-based targeting
// into a resolvedEdit ready for Phase 2 application. start/end are byte
// offsets into origBody (already resolved from line numbers).
//
// Returns start, end, replacement. Unknown ops return an empty replacement
// with start==end==0 (the caller should have rejected them earlier, but we
// don't panic on surprise input).
func buildLineModeEdit(p domain.FunctionPatch, origBody string, start, end int) (int, int, string) {
	switch p.Op {
	case domain.PatchOpReplace:
		repl := p.Replace
		// Re-indent to match the original line, like the match branch does.
		if repl != "" && !startsWithWhitespace(repl) {
			if indent := lineIndent(origBody, start); indent != "" && start == lineStartOffset(origBody, start) {
				repl = reIndentReplacement(repl, indent)
			}
		}
		// Ensure trailing newline if we're replacing a line range and the
		// replacement doesn't already end with one.
		if end > start && origBody[end-1] == '\n' && (repl == "" || repl[len(repl)-1] != '\n') {
			repl += "\n"
		}
		return start, end, repl
	case domain.PatchOpInsertBefore:
		indent := lineIndent(origBody, start)
		line := indent + strings.TrimSpace(p.Code) + "\n"
		return start, start, line
	case domain.PatchOpInsertAfter:
		indent := lineIndent(origBody, start)
		line := indent + strings.TrimSpace(p.Code) + "\n"
		return end, end, line
	case domain.PatchOpDelete:
		return start, end, ""
	}
	// PatchOpWrap in line mode isn't defined — wrap is always about
	// wrapping a matched substring, which doesn't map cleanly to a line
	// range. Callers using line mode should pick a different op.
	return start, end, p.Replace
}
