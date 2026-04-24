package commands

import (
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/scanner"
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

	// Locate the target function. When req.Identifier contains '>', the
	// trailing segments are a closure path (closure[N]>closure[M]...) that
	// drills into the Nth *ast.FuncLit of the body resolved so far. The
	// parent identifier before the first '>' is resolved like today.
	parentID, closurePath, pathErr := parseClosurePath(req.Identifier)
	if pathErr != nil {
		return domain.PatchFunctionResult{}, &domain.Error{
			Code:    "NODE_NOT_FOUND",
			Message: fmt.Sprintf("invalid identifier %q: %v", req.Identifier, pathErr),
		}
	}
	recvTarget, nameTarget := parseIdentifier(parentID)
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
		// Check whether the name matches a var or func-returning expression
		// (e.g. a Cobra command constructor like newSyncCmd). If so, emit a
		// targeted hint so the agent doesn't burn two attempts.
		hint := closureFuncHint(f, nameTarget)
		msg := fmt.Sprintf("function %q not found in %s", req.Identifier, req.FilePath)
		if hint != "" {
			msg += "\n" + hint
		}
		return domain.PatchFunctionResult{}, &domain.Error{Code: "NODE_NOT_FOUND", Message: msg}
	}
	if targetFn.Body == nil {
		return domain.PatchFunctionResult{}, &domain.Error{
			Code:    "NODE_NOT_FOUND",
			Message: fmt.Sprintf("function %q has no body", req.Identifier),
		}
	}

	// Walk closurePath (if any) to drill into the Nth FuncLit of each body.
	targetBody := targetFn.Body
	for depth, idx := range closurePath {
		children := directChildClosures(targetBody)
		if idx >= len(children) {
			var rangeMsg string
			if len(children) == 0 {
				rangeMsg = "no closures in this body"
			} else {
				rangeMsg = fmt.Sprintf("0..%d", len(children)-1)
			}
			return domain.PatchFunctionResult{}, &domain.Error{
				Code:    "NODE_NOT_FOUND",
				Message: fmt.Sprintf("closure index %d out of range (%s) at depth %d of %s", idx, rangeMsg, depth+1, req.Identifier),
			}
		}
		if children[idx].Body == nil {
			return domain.PatchFunctionResult{}, &domain.Error{
				Code:    "NODE_NOT_FOUND",
				Message: fmt.Sprintf("closure[%d] at depth %d in %s has no body", idx, depth+1, req.Identifier),
			}
		}
		targetBody = children[idx].Body
	}

	lbraceOff := fset.Position(targetBody.Lbrace).Offset // offset of '{'
	bodyStartLine := fset.Position(targetBody.Lbrace).Line
	rbraceOff := fset.Position(targetBody.Rbrace).Offset // offset of '}'
	origBody := string(src[lbraceOff+1 : rbraceOff])

	// Build the closure-exclusion range set used to restrict text matches to
	// the body's own top-level region (ignoring anything nested inside a
	// *ast.FuncLit). When req.IncludeNested is true, we skip this and let
	// matches land anywhere, including inside closures (legacy behavior).
	var closureRanges [][2]int
	if !req.IncludeNested {
		closureRanges = collectClosureRanges(fset, targetBody, lbraceOff+1)
	}

	var warnings []string
	var autoLifts []domain.AutoLiftInfo
	// Pending insert-context entries filled in after edits are applied.
	type pendingInsertCtx struct {
		liftIndex     int // index into autoLifts
		bodyRelStart  int // body-relative offset where insertion begins
		insertedBytes int // length of the inserted text (lines + newline)
		insertedLines int // number of lines inserted
	}
	var pendingCtx []pendingInsertCtx
	// Phase 1: resolve all patches against the original body.
	type resolvedEdit struct {
		start, end  int    // byte offsets relative to origBody start
		replacement string // text to substitute
	}
	edits := make([]resolvedEdit, len(req.Patches))
	var sigEdits []signatureEdit
	var errs []string

	type afterFuncEdit struct {
		code string
	}
	afterFuncEdits := map[int]afterFuncEdit{}
	for i, p := range req.Patches {
		if p.Op == domain.PatchOpSetSignature {
			ses, sigErr := resolveSignatureEdit(fset, targetFn, src, p.Params, p.Returns)
			if sigErr != nil {
				errs = append(errs, fmt.Sprintf("patch #%d (set_signature): %v", i+1, sigErr))
				continue
			}
			sigEdits = append(sigEdits, ses...)
			continue
		}
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
			// insert_after on the function declaration line means
			// "insert after the closing } of the function". Detect this
			// before resolveBodyLineRange so it works for one-liners too
			// (where the declaration line equals bodyStartLine).
			if p.Op == domain.PatchOpInsertAfter && p.AtLine > 0 && p.AtLine == fset.Position(targetFn.Pos()).Line {
				afterFuncEdits[i] = afterFuncEdit{code: p.Code}
				continue
			}
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
			// Enforce the top-level scope on line-mode too: if the resolved
			// range lies entirely inside a nested closure, refuse the edit.
			// Pass include_nested=true or use identifier 'Parent>closure[N]'
			// to target a closure explicitly.
			if len(closureRanges) > 0 && hitInsideAnyRange(start, end, closureRanges) {
				errs = append(errs, fmt.Sprintf("patch #%d (%s): line range %d-%d is inside a nested closure of %s. Pass include_nested=true, or use identifier 'Parent>closure[N]' to edit a closure directly.", i+1, p.Op, fromL, toL, req.Identifier))
				continue
			}
			if p.Op == domain.PatchOpInsertBefore || p.Op == domain.PatchOpInsertAfter {
				plan := resolveInsertAnchor(fset, targetFn, origBody, lbraceOff+1, bodyStartLine, i+1, p.Op, p.Code, start)
				edits[i] = resolvedEdit{start: plan.Start, end: plan.End, replacement: plan.Line}
				if plan.Lift != nil {
					autoLifts = append(autoLifts, *plan.Lift)
					pendingCtx = append(pendingCtx, pendingInsertCtx{
						liftIndex:     len(autoLifts) - 1,
						bodyRelStart:  plan.Start,
						insertedBytes: len(plan.Line),
						insertedLines: plan.LineCount,
					})
				}
				continue
			}
			if p.Op == domain.PatchOpReplace && replaceEndsWithBareClosingBrace(p.Replace) {
				errs = append(errs, fmt.Sprintf("patch #%d (replace): replacement has more closing braces than opening ones — it would consume the function's own '}'. Ensure braces in the replacement are balanced.", i+1))
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
		// Restrict text-match hits to the top-level body region by dropping
		// any hit that lies entirely inside a nested closure's body.
		allHits := hits
		hits = filterHitsByRanges(hits, closureRanges)

		if len(hits) == 0 {
			needle := p.Match
			if needle == "" {
				needle = p.MatchRegex
			}
			var msg string
			if len(allHits) > 0 && len(closureRanges) > 0 {
				// The match exists but lies inside a nested closure — give a direct hint.
				msg = fmt.Sprintf("patch #%d (%s %q): match found but is inside a nested closure of %s. "+
					"Use identifier %q to target the closure directly, or pass include_nested=true.",
					i+1, p.Op, needle, req.Identifier, req.Identifier+">closure[0]")
			} else {
				msg = fmt.Sprintf("patch #%d (%s %q): no match found in body of %s",
					i+1, p.Op, needle, req.Identifier)
			}
			if len(allHits) == 0 {
				if suggestions := suggestClosestLines(origBody, needle, 3); suggestions != "" {
					msg += "\n  Closest lines in body:\n" + suggestions
				}
			}
			errs = append(errs, msg)
			continue
		}
		if len(hits) > 1 && p.Occurrence == 0 {
			// For insert_before/insert_after: if every hit would auto-lift to
			// the SAME top-level statement, the lifted position is
			// unambiguous even though the raw anchor matched multiple times.
			// Fall through with a synthetic idx=0 in that case.
			if (p.Op == domain.PatchOpInsertBefore || p.Op == domain.PatchOpInsertAfter) && liftsAgree(fset, targetFn, lbraceOff+1, hits) {
				// proceed with the first hit; the lift logic will anchor
				// at the shared top-level statement.
			} else {
				var candidates []string
				shown := hits
				if len(shown) > maxCandidatesShown {
					shown = shown[:maxCandidatesShown]
				}
				firstLine := bodyStartLine + strings.Count(origBody[:shown[0][0]], "\n")
				for _, h := range shown {
					line := extractLine(origBody, h[0])
					lineNum := bodyStartLine + strings.Count(origBody[:h[0]], "\n")
					candidates = append(candidates, fmt.Sprintf("  L%d: %s", lineNum, strings.TrimSpace(line)))
				}
				trailer := ""
				if len(hits) > maxCandidatesShown {
					trailer = fmt.Sprintf("\n  ... (%d more)", len(hits)-maxCandidatesShown)
				}
				msg := fmt.Sprintf(
					"patch #%d (%s %q): matched %d times in body of %s. Disambiguate with occurrence: 1..%d. Candidates:\n%s%s\nHint: retry with at_line: %d (or use occurrence: 1..%d)",
					i+1, p.Op, p.Match+p.MatchRegex, len(hits), req.Identifier, len(hits), strings.Join(candidates, "\n"), trailer, firstLine, len(hits),
				)
				if p.Op == domain.PatchOpInsertBefore || p.Op == domain.PatchOpInsertAfter {
					if liftCands := ambiguousLiftCandidates(fset, targetFn, src, lbraceOff+1, hits); len(liftCands) > 1 {
						msg += "\nAuto-lift candidates (different top-level statements):\n  " + strings.Join(liftCands, "\n  ")
					}
				}
				errs = append(errs, msg)
				continue
			}
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
			// Warn about unreplaced leftover matches so the agent knows to re-run
			// if they meant all of them (pass-6 friction).
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
			if replaceEndsWithBareClosingBrace(p.Replace) {
				errs = append(errs, fmt.Sprintf("patch #%d (replace): replacement has more closing braces than opening ones — it would consume the function's own '}'. Ensure braces in the replacement are balanced.", i+1))
				continue
			}
			repl := p.Replace
			// Re-apply the original line's indentation when the replacement has none,
			// so callers don't need to reproduce leading whitespace.
			if repl != "" && !startsWithWhitespace(repl) {
				if indent := lineIndent(origBody, hit[0]); indent != "" && hit[0] == lineStartOffset(origBody, hit[0]) {
					repl = reIndentReplacement(repl, indent)
				}
			}
			edits[i] = resolvedEdit{start: hit[0], end: hit[1], replacement: repl}

		case domain.PatchOpInsertBefore, domain.PatchOpInsertAfter:
			plan := resolveInsertAnchor(fset, targetFn, origBody, lbraceOff+1, bodyStartLine, i+1, p.Op, p.Code, hit[0])
			edits[i] = resolvedEdit{start: plan.Start, end: plan.End, replacement: plan.Line}
			if plan.Lift != nil {
				autoLifts = append(autoLifts, *plan.Lift)
				pendingCtx = append(pendingCtx, pendingInsertCtx{
					liftIndex:     len(autoLifts) - 1,
					bodyRelStart:  plan.Start,
					insertedBytes: len(plan.Line),
					insertedLines: plan.LineCount,
				})
			}

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
			errs = append(errs, fmt.Sprintf("patch #%d: unknown op %q (must be replace, insert_before, insert_after, delete, wrap, set_signature)", i+1, p.Op))
		}
	}

	if len(errs) > 0 {
		msg := strings.Join(errs, "\n")
		// Include the full function body with line numbers so the agent can
		// correct its patch without a follow-up symbol call.
		funcStart := fset.Position(targetFn.Pos())
		funcEnd := fset.Position(targetFn.End())
		if body := formatNumberedSource(src, funcStart.Offset, funcEnd.Offset, funcStart.Line); body != "" {
			msg += fmt.Sprintf("\n\nCurrent body of %s (lines %d-%d):\n%s", req.Identifier, funcStart.Line, funcEnd.Line, body)
		}
		msg += "\nHint: retry with from_line/to_line targeting using the line numbers above."
		return domain.PatchFunctionResult{}, &domain.Error{
			Code:    "PATCH_FAILED",
			Message: msg,
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

	// Reject a patch that would wipe the entire function body. An empty body
	// is almost never intentional from patch_function — use delete op or
	// patch target=file for wholesale replacement.
	if strings.TrimSpace(string(newBody)) == "" && strings.TrimSpace(origBody) != "" {
		return domain.PatchFunctionResult{}, &domain.Error{
			Code:    "PATCH_FAILED",
			Message: fmt.Sprintf("patch would erase the entire body of %s — use op:delete to remove individual statements, or patch target=file for a full rewrite", req.Identifier),
		}
	}

	// Reassemble the full file.
	newSrc := make([]byte, 0, len(src)+len(newBody)-len(origBody))
	newSrc = append(newSrc, src[:lbraceOff+1]...)
	newSrc = append(newSrc, newBody...)
	newSrc = append(newSrc, src[rbraceOff:]...)

	// Apply after-func insertions: code that goes immediately after the closing
	// } of the function. These are collected from insert_after patches whose
	// at_line pointed at the function declaration line.
	if len(afterFuncEdits) > 0 {
		// The '}' in newSrc sits at lbraceOff+1+len(newBody) because
		// newSrc = src[:lbraceOff+1] + newBody + src[rbraceOff:].
		// Advance past the '}' and any immediately following newline.
		insertOff := lbraceOff + 1 + len(newBody) + 1 // past '}'
		if insertOff < len(newSrc) && newSrc[insertOff] == '\n' {
			insertOff++ // past the newline after '}'
		}
		// Collect patch indices in order so output is deterministic.
		afterKeys := make([]int, 0, len(afterFuncEdits))
		for k := range afterFuncEdits {
			afterKeys = append(afterKeys, k)
		}
		sort.Ints(afterKeys)
		for _, k := range afterKeys {
			code := afterFuncEdits[k].code
			if !strings.HasSuffix(code, "\n") {
				code += "\n"
			}
			newSrc = append(newSrc[:insertOff], append([]byte(code), newSrc[insertOff:]...)...)
			insertOff += len(code)
		}
	}
	// Apply signature edits (set_signature). Their offsets are absolute in src
	// and unchanged in newSrc because sig edits sit before the body range.
	if len(sigEdits) > 0 {
		sort.Slice(sigEdits, func(a, b int) bool { return sigEdits[a].start > sigEdits[b].start })
		for _, se := range sigEdits {
			newSrc = append(newSrc[:se.start], append([]byte(se.replacement), newSrc[se.end:]...)...)
		}
		if formatted, fmtErr := format.Source(newSrc); fmtErr == nil {
			newSrc = formatted
		}
	}
	if err := validateGoSource(req.FilePath, newSrc); err != nil {
		return domain.PatchFunctionResult{}, err
	}
	if dirErr := checkDirectivesIntact(newSrc, req.FilePath); dirErr != nil {
		return domain.PatchFunctionResult{}, dirErr
	}

	diff := diffStrings(req.FilePath, string(src), string(newSrc))

	// Fill in Context for each AutoLift using the assembled newSrc. The edits
	// were applied high-to-low on origBody, so each pending entry's
	// body-relative offset is still valid on newBody; translate to a file
	// line by counting newlines up to that point.
	if len(pendingCtx) > 0 {
		// Sort ascending by start so earlier inserts get counted first for line math.
		sort.Slice(pendingCtx, func(a, b int) bool { return pendingCtx[a].bodyRelStart < pendingCtx[b].bodyRelStart })
		// Use newBody directly; compute the line where each insert landed.
		for _, pc := range pendingCtx {
			// The inserted text ends at bodyRelStart+insertedBytes-1 in newBody.
			// bodyRelStart is the offset BEFORE the insertion in the edited body.
			// Convert to a file-absolute line: lines in file up to lbraceOff+1+bodyRelStart+1.
			fileOff := lbraceOff + 1 + pc.bodyRelStart
			if fileOff > len(newSrc) {
				fileOff = len(newSrc)
			}
			insertLine := 1 + strings.Count(string(newSrc[:fileOff]), "\n")
			// If the insert happened at a line-end offset, the inserted
			// line is on the NEXT line number.
			if fileOff > 0 && fileOff-1 < len(newSrc) && newSrc[fileOff-1] == '\n' {
				// already correct
			} else if fileOff < len(newSrc) && newSrc[fileOff] != '\n' {
				// insertion was mid-line; treat as next line
				insertLine++
			}
			ctxLines := contextAroundInsertion(newSrc, insertLine, pc.insertedLines, 10)
			autoLifts[pc.liftIndex].Context = ctxLines
		}
	}

	if req.Preview {
		return domain.PatchFunctionResult{Diff: diff, Applied: len(req.Patches), Preview: true, Warnings: warnings, AutoLifts: autoLifts}, nil
	}

	addedImports, err := h.fs.WriteFile(ctx, req.FilePath, newSrc)
	if err != nil {
		return domain.PatchFunctionResult{}, &domain.Error{Code: "WRITE_ERROR", Message: "failed to write file", Err: err}
	}

	return domain.PatchFunctionResult{Diff: diff, Applied: len(req.Patches), AddedImports: addedImports, Warnings: warnings, AutoLifts: autoLifts}, nil
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
// Matching is attempted at four granularities:
//  1. whole-line  (match trims to the entire trimmed line)
//  2. sub-string  (match found anywhere within a line after normalizing spaces)
//  3. multi-line  (match spans multiple lines after collapsing all whitespace runs)
//  4. token-based (go/scanner tokenization, ignoring whitespace and comments)
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
	if len(hits) > 0 {
		return hits
	}

	// 4: token-based: scan both strings with go/scanner and match on
	// token sequences, ignoring all whitespace and comment differences.
	return FindTokenMatches(body, match)
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

// closureSegRegexp matches a closure path segment like `closure[3]`.
var closureSegRegexp = regexpMustCompile(`^closure\[(\d+)\]$`)

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

// replaceEndsWithBareClosingBrace reports whether repl's last non-empty line
// is a bare '}'. Such a replacement inside a function body would introduce a
// second closing brace that conflicts with the function's own '}'.
func replaceEndsWithBareClosingBrace(repl string) bool {
	// Count only top-level (unquoted, uncommented) braces to detect an
	// unbalanced replacement that would consume the function's own '}'.
	depth := 0
	inString := false
	var stringChar byte
	for i := 0; i < len(repl); i++ {
		ch := repl[i]
		if inString {
			switch ch {
			case '\\':
				i++ // skip escaped char
			case stringChar:
				inString = false
			}
			continue
		}
		switch ch {
		case '"', '`', '\'':
			inString = true
			stringChar = ch
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	// depth < 0 means more closing braces than opening ones — the replacement
	// would consume brace(s) from the surrounding function body.
	return depth < 0
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

// formatNumberedSource returns the file region between startOff..endOff
// with file-absolute line numbers (one per line), so the agent can see
// the current content and retry a failed patch without a follow-up symbol call.
func formatNumberedSource(src []byte, startOff, endOff, startLine int) string {
	if startOff < 0 || endOff > len(src) || startOff >= endOff {
		return ""
	}
	lines := strings.Split(string(src[startOff:endOff]), "\n")
	var b strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&b, " %d: %s\n", startLine+i, line)
	}
	return strings.TrimRight(b.String(), "\n")
}

// tokInfo represents a scanned Go token with its byte range in the source.
type tokInfo struct {
	text string
	pos  int
	end  int
}

// scanTokens tokenizes a Go source fragment using go/scanner.
// Comments and implicit semicolons are omitted so that cosmetic
// differences between match and body don't cause false negatives.
func scanTokens(s string) []tokInfo {
	src := []byte(s)
	fset := token.NewFileSet()
	file := fset.AddFile("", -1, len(src))
	var sc scanner.Scanner
	sc.Init(file, src, nil, 0)
	var result []tokInfo
	for {
		pos, tok, lit := sc.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.SEMICOLON && lit == "\n" {
			continue
		}
		off := file.Offset(pos)
		text := lit
		if text == "" {
			text = tok.String()
		}
		result = append(result, tokInfo{text: text, pos: off, end: off + len(text)})
	}
	return result
}

// FindTokenMatches finds byte ranges in body where the Go token sequence
// from match appears. Used as a fallback when whitespace normalization
// alone isn't enough — handles comment differences and formatting
// divergences that produce identical token streams. Returned ranges are
// extended to full-line boundaries for consistent indentation handling.
func FindTokenMatches(body, match string) [][2]int {
	bodyToks := scanTokens(body)
	matchToks := scanTokens(match)
	if len(matchToks) == 0 || len(bodyToks) < len(matchToks) {
		return nil
	}
	var hits [][2]int
	for i := 0; i <= len(bodyToks)-len(matchToks); i++ {
		found := true
		for j := range matchToks {
			if bodyToks[i+j].text != matchToks[j].text {
				found = false
				break
			}
		}
		if found {
			start := bodyToks[i].pos
			end := bodyToks[i+len(matchToks)-1].end
			// Extend to full-line boundaries so indentation handling
			// works the same as whitespace-normalized matches.
			for start > 0 && body[start-1] != '\n' {
				start--
			}
			for end < len(body) && body[end] != '\n' {
				end++
			}
			hits = append(hits, [2]int{start, end})
		}
	}
	return hits
}

// resolveSignatureEdit computes the absolute file-offset edits needed to
// rewrite the params and/or results list of a function or method while
// leaving the body, name, receiver, and any generic type-parameter block
// (e.g. [T any]) intact.
//
// At least one of newParams or newReturns must be non-empty. newParams is
// expected to include its surrounding parens (e.g. "(ctx context.Context, x int)").
// newReturns is rendered exactly as supplied (a single type like "error"
// or a parenthesised list like "([]byte, error)" both work).
//
// Each returned signatureEdit describes a byte range in src to splice.
// When both params and returns are rewritten the function returns two
// separate edits; applied descending they do not overlap.
func resolveSignatureEdit(fset *token.FileSet, fn *ast.FuncDecl, src []byte, newParams, newReturns string) ([]signatureEdit, error) {
	if strings.TrimSpace(newParams) == "" && strings.TrimSpace(newReturns) == "" {
		return nil, fmt.Errorf("at least one of params or returns must be provided")
	}
	if fn.Type == nil || fn.Type.Params == nil {
		return nil, fmt.Errorf("function has no parameter list")
	}

	// Validate supplied params/returns by parsing a synthetic function
	// declaration. We keep the receiver off so methods and free functions
	// validate the same way; the existing FuncDecl already encodes the
	// receiver and we don't touch it.
	probeParams := strings.TrimSpace(newParams)
	if probeParams == "" {
		probeParams = "()"
	}
	// Reject a bare (missing parens) params input so we don't accidentally
	// turn "ctx context.Context, x int" into a syntax error after splicing.
	if probeParams[0] != '(' {
		return nil, fmt.Errorf("params must start with '(' and include the surrounding parens (got %q)", newParams)
	}
	probeSrc := "package p\nfunc _" + probeParams
	if strings.TrimSpace(newReturns) != "" {
		probeSrc += " " + newReturns
	}
	probeSrc += " {}\n"
	probeFset := token.NewFileSet()
	if _, perr := parser.ParseFile(probeFset, "", probeSrc, 0); perr != nil {
		if newReturns != "" && newParams != "" {
			return nil, fmt.Errorf("invalid params/returns: %w", perr)
		}
		if newReturns != "" {
			return nil, fmt.Errorf("invalid returns %q: %w", newReturns, perr)
		}
		return nil, fmt.Errorf("invalid params %q: %w", newParams, perr)
	}

	paramsOpen := fset.Position(fn.Type.Params.Opening).Offset
	paramsClose := fset.Position(fn.Type.Params.Closing).Offset + 1 // exclusive
	sigEnd := fset.Position(fn.Type.End()).Offset                   // exclusive

	var edits []signatureEdit
	if newParams != "" {
		edits = append(edits, signatureEdit{start: paramsOpen, end: paramsClose, replacement: newParams})
	}
	if newReturns != "" {
		// returns range spans from just after the params' ')' to the end of
		// the signature. If the function currently has no results, the range
		// is empty (paramsClose == sigEnd) and we insert " <returns>".
		edits = append(edits, signatureEdit{start: paramsClose, end: sigEnd, replacement: " " + newReturns})
	}
	return edits, nil
}

// signatureEdit describes a byte-range replacement used by the
// set_signature patch op. Offsets are absolute in the original file source.
type signatureEdit struct {
	start, end  int
	replacement string
}

// closureFuncHint returns a non-empty hint string when name matches a var/const
// in file f whose value contains a func literal (e.g. a Cobra command constructor
// built with cobra.Command{RunE: func(...){...}}). It tells the agent which
// >closure[N] suffix to use instead of a bare identifier.
func closureFuncHint(f *ast.File, name string) string {
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, id := range vs.Names {
				if id.Name != name {
					continue
				}
				if i >= len(vs.Values) {
					continue
				}
				if countFuncLits(vs.Values[i]) > 0 {
					return fmt.Sprintf("%q is a var/const, not a named func declaration. "+
						"If it contains a closure body you want to edit, use identifier %q.",
						name, name+">closure[0]")
				}
			}
		}
	}
	// Also check func declarations that return a struct literal containing
	// func literals (e.g. func newCmd() *cobra.Command { return &cobra.Command{RunE: func...} }).
	// These ARE named funcs and resolve fine, so no hint needed for them —
	// they use >closure[N] syntax already. Only emit hints for var/const mismatches.
	return ""
}

// countFuncLits returns the number of *ast.FuncLit nodes directly or
// indirectly contained within expr.
func countFuncLits(expr ast.Expr) int {
	if expr == nil {
		return 0
	}
	count := 0
	ast.Inspect(expr, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			count++
		}
		return true
	})
	return count
}

// parseClosurePath splits an identifier of the form Parent>closure[N]>closure[M]...
// into the parent identifier and the sequence of 0-based closure indices to
// traverse. When id contains no '>', the second return is nil and err is nil —
// callers use the parent identifier as-is. Returns an error when a segment
// does not match closure[<digits>] or when the index is negative.
func parseClosurePath(id string) (string, []int, error) {
	if !strings.Contains(id, ">") {
		return id, nil, nil
	}
	parts := strings.Split(id, ">")
	parent := parts[0]
	if parent == "" {
		return "", nil, fmt.Errorf("identifier has empty parent before '>'")
	}
	indices := make([]int, 0, len(parts)-1)
	for _, seg := range parts[1:] {
		m := closureSegRegexp.FindStringSubmatch(seg)
		if m == nil {
			return "", nil, fmt.Errorf("invalid closure segment %q: expected closure[N] with N>=0", seg)
		}
		n := atoi(m[1])
		if n < 0 {
			return "", nil, fmt.Errorf("invalid closure segment %q: index must be >= 0", seg)
		}
		indices = append(indices, n)
	}
	return parent, indices, nil
}

// directChildClosures returns every *ast.FuncLit whose body lies strictly
// inside body (the argument), ordered by their appearance in the source
// (AST walk order). Closures nested inside another closure are NOT returned
// — only the immediate children, because that matches how the identifier
// path drills down one level at a time.
func directChildClosures(body *ast.BlockStmt) []*ast.FuncLit {
	if body == nil {
		return nil
	}
	var out []*ast.FuncLit
	inspect := func(n ast.Node) {
		ast.Inspect(n, func(nn ast.Node) bool {
			if nn == n {
				return true
			}
			if fl, ok := nn.(*ast.FuncLit); ok {
				out = append(out, fl)
				// Do not descend into this FuncLit; its closures are grandchildren.
				return false
			}
			return true
		})
	}
	inspect(body)
	return out
}

// collectClosureRanges returns body-relative [start,end] byte ranges covering
// every *ast.FuncLit whose body is strictly nested inside body (the argument).
// bodyContentOff is the file offset of the first byte of body.List (i.e. the
// byte just after the body's opening '{'). The ranges returned are relative
// to that position, which is the same coordinate space as origBody in
// PatchFunction.
func collectClosureRanges(fset *token.FileSet, body *ast.BlockStmt, bodyContentOff int) [][2]int {
	if body == nil {
		return nil
	}
	var ranges [][2]int
	ast.Inspect(body, func(n ast.Node) bool {
		fl, ok := n.(*ast.FuncLit)
		if !ok {
			return true
		}
		if fl.Body == nil {
			return false
		}
		// We exclude the FuncLit's inner body (between '{' and '}'), NOT the
		// 'func(...) {' prefix — so that an anchor matching text on the
		// 'func(' line itself is still visible to the outer body.
		start := fset.Position(fl.Body.Lbrace).Offset + 1 - bodyContentOff
		end := fset.Position(fl.Body.Rbrace).Offset - bodyContentOff
		if start < 0 {
			start = 0
		}
		if end > start {
			ranges = append(ranges, [2]int{start, end})
		}
		// Do not descend: deeper closures are already covered by this range.
		return false
	})
	return ranges
}

// hitInsideAnyRange reports whether the [hitStart,hitEnd] byte range lies
// entirely inside one of the excluded ranges. Touching a boundary counts
// as inside so anchors cannot straddle a closure edge and land half in,
// half out.
func hitInsideAnyRange(hitStart, hitEnd int, ranges [][2]int) bool {
	for _, r := range ranges {
		if hitStart >= r[0] && hitEnd <= r[1] {
			return true
		}
	}
	return false
}

// filterHitsByRanges returns the subset of hits that fall OUTSIDE every
// exclusion range. When ranges is empty, hits is returned unchanged.
func filterHitsByRanges(hits [][2]int, ranges [][2]int) [][2]int {
	if len(ranges) == 0 || len(hits) == 0 {
		return hits
	}
	out := make([][2]int, 0, len(hits))
	for _, h := range hits {
		if !hitInsideAnyRange(h[0], h[1], ranges) {
			out = append(out, h)
		}
	}
	return out
}
