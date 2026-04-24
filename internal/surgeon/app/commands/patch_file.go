package commands

import (
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// PatchFile applies whole-file text substitutions to a Go source file,
// re-parses the result to guarantee syntactic validity, runs gofmt and
// writes the file (or returns the diff in preview mode).
//
// Each patch is either a literal match (all occurrences replaced) or a
// regex match (RE2 with $1/$2 submatch substitutions). Patches apply
// sequentially — each patch sees the text produced by the previous one,
// which is the intuitive "apply rename, then tweak" behavior.
//
// Safety contract:
//   - file must be a .go file (callers are expected to pre-validate, but we
//     double-check here so misuse from non-MCP callers is still caught).
//   - an empty Patches list is rejected (use update file for wholesale
//     rewrites).
//   - each patch must specify exactly one of Match or MatchRegex.
//   - after all patches apply the source is re-parsed via go/format.Source.
//     A parse failure aborts the write and returns PATCH_PRODUCES_INVALID_GO
//     with the parser diagnostic; the file on disk is untouched.
//   - zero-match patches are allowed and recorded as Warnings (so callers
//     can issue defensive renames without a hard error).
func (h *ExecutePlanHandler) PatchFile(ctx context.Context, req domain.PatchFileRequest) (domain.PatchFileResult, error) {
	if !strings.HasSuffix(req.FilePath, ".go") {
		return domain.PatchFileResult{}, &domain.Error{
			Code:    "INVALID_ARGUMENT",
			Message: fmt.Sprintf("patch_file only operates on .go files, got %q", req.FilePath),
		}
	}
	if len(req.Patches) == 0 {
		return domain.PatchFileResult{}, &domain.Error{
			Code:    "INVALID_ARGUMENT",
			Message: "patch_file requires at least one patch",
		}
	}
	scope := req.Scope
	if scope == "" {
		scope = "all"
	}
	switch scope {
	case "all", "code_only", "identifiers_only":
	default:
		return domain.PatchFileResult{}, &domain.Error{
			Code:    "INVALID_ARGUMENT",
			Message: "patch_file: scope must be all, code_only, or identifiers_only",
		}
	}

	src, err := h.fs.ReadFile(ctx, req.FilePath)
	if err != nil {
		return domain.PatchFileResult{}, &domain.Error{Code: "READ_ERROR", Message: "failed to read file", Err: err}
	}

	// Phase 1: validate each patch up-front so errors are reported as a
	// single batch rather than one-per-turn (mirrors patch_function's style).
	var errs []string
	compiled := make([]*regexp.Regexp, len(req.Patches))
	for i, p := range req.Patches {
		if p.Match == "" && p.MatchRegex == "" {
			errs = append(errs, fmt.Sprintf("patch #%d: match or match_regex is required", i+1))
			continue
		}
		if p.Match != "" && p.MatchRegex != "" {
			errs = append(errs, fmt.Sprintf("patch #%d: match and match_regex are mutually exclusive", i+1))
			continue
		}
		if p.MatchLiteral && p.Match != "" {
			errs = append(errs, fmt.Sprintf("patch #%d: match_literal requires match_regex, not match", i+1))
			continue
		}
		if p.Occurrence < 0 {
			errs = append(errs, fmt.Sprintf("patch #%d: occurrence must be >= 0", i+1))
			continue
		}
		if p.MatchRegex != "" {
			pattern := p.MatchRegex
			if p.MatchLiteral {
				pattern = regexp.QuoteMeta(p.MatchRegex)
			}
			re, regErr := regexp.Compile(pattern)
			if regErr != nil {
				errs = append(errs, fmt.Sprintf("patch #%d: invalid match_regex: %v", i+1, regErr))
				continue
			}
			compiled[i] = re
		}
	}
	if len(errs) > 0 {
		return domain.PatchFileResult{}, &domain.Error{
			Code:    "PATCH_FAILED",
			Message: strings.Join(errs, "\n"),
		}
	}

	// Phase 2: apply patches sequentially. Each patch sees the result of the
	// previous one, which lets callers stack related renames (the literal
	// one first, then a targeted regex cleanup, etc.).
	working := string(src)
	hits := make([]int, len(req.Patches))
	var warnings []string

	for i, p := range req.Patches {
		needle := p.Match
		if needle == "" {
			needle = p.MatchRegex
		}
		// Re-parse the CURRENT working source before every patch so exclusion
		// and identifier ranges reflect the result of prior substitutions.
		var (
			excluded []rangePair
			idents   []rangePair
		)
		if scope != "all" {
			var perr error
			excluded, idents, perr = collectScopeRanges(req.FilePath, working)
			if perr != nil {
				// If the intermediate source is unparseable, fall back to scope=all
				// for this patch — the final re-parse/gofmt guard still catches
				// invalid output. We record a warning so callers know.
				warnings = append(warnings, fmt.Sprintf("patch #%d: scope=%s filter skipped — intermediate source is not parseable (%v)", i+1, scope, perr))
				scope = "all"
			}
		}

		var (
			accepted [][2]int
			filtered [][2]int
			replaces []string
		)
		if p.MatchRegex != "" {
			re := compiled[i]
			matchIndex := 0
			for _, m := range re.FindAllStringSubmatchIndex(working, -1) {
				start, end := m[0], m[1]
				if scope != "all" && !rangeAllowed(start, end, working, scope, excluded, idents) {
					filtered = append(filtered, [2]int{start, end})
					continue
				}
				matchIndex++
				if p.Occurrence > 0 && matchIndex != p.Occurrence {
					continue
				}
				accepted = append(accepted, [2]int{start, end})
				// Expand backrefs using the captured groups for THIS match.
				replaces = append(replaces, string(re.ExpandString(nil, p.Replace, working, m)))
			}
		} else {
			matchIndex := 0
			for from := 0; from < len(working); {
				idx := strings.Index(working[from:], p.Match)
				if idx < 0 {
					break
				}
				start := from + idx
				end := start + len(p.Match)
				if scope != "all" && !rangeAllowed(start, end, working, scope, excluded, idents) {
					filtered = append(filtered, [2]int{start, end})
				} else {
					matchIndex++
					if p.Occurrence > 0 && matchIndex != p.Occurrence {
						from = end
						continue
					}
					accepted = append(accepted, [2]int{start, end})
					replaces = append(replaces, p.Replace)
				}
				from = end
			}
		}

		total := len(accepted) + len(filtered)
		hits[i] = len(accepted)
		// Capture filtered line numbers BEFORE replacements mutate offsets.
		filteredLines := formatFilteredLines(working, filtered)
		if len(accepted) > 0 {
			working = applyRangeReplacements(working, accepted, replaces)
		}

		switch {
		case total == 0:
			warnings = append(warnings, fmt.Sprintf("patch #%d: zero matches for %q — no changes from this patch", i+1, needle))
		case len(accepted) == 0 && len(filtered) > 0:
			warnings = append(warnings, fmt.Sprintf("patch #%d: %d occurrences matched but all filtered out by scope=%s; no changes from this patch", i+1, len(filtered), scope))
		case len(filtered) > 0:
			warnings = append(warnings, fmt.Sprintf("patch #%d: %d occurrences filtered out by scope=%s (lines: %s)", i+1, len(filtered), scope, filteredLines))
		}
	}

	newSrc := []byte(working)

	// Phase 3: gofmt. go/format.Source both normalizes whitespace and
	// serves as our parse check — a parse failure surfaces here. We surface
	// the error with the structured code the agent expects.
	formatted, fmtErr := format.Source(newSrc)
	if fmtErr != nil {
		// Re-use the existing validator for a pretty snippet.
		if vErr := validateGoSource(req.FilePath, newSrc); vErr != nil {
			return domain.PatchFileResult{}, vErr
		}
		// Belt-and-braces: if validateGoSource somehow passes but format.Source
		// doesn't, surface the raw gofmt error.
		return domain.PatchFileResult{}, &domain.Error{
			Code:    "PATCH_PRODUCES_INVALID_GO",
			Message: "patch would produce invalid Go — rejected before writing: " + fmtErr.Error(),
		}
	}

	diff := diffStrings(req.FilePath, string(src), string(formatted))

	if req.Preview {
		return domain.PatchFileResult{
			Diff:     diff,
			Applied:  len(req.Patches),
			Hits:     hits,
			Preview:  true,
			Warnings: warnings,
		}, nil
	}

	addedImports, err := h.fs.WriteFile(ctx, req.FilePath, formatted)
	if err != nil {
		return domain.PatchFileResult{}, &domain.Error{Code: "WRITE_ERROR", Message: "failed to write file", Err: err}
	}

	return domain.PatchFileResult{
		Diff:         diff,
		Applied:      len(req.Patches),
		Hits:         hits,
		AddedImports: addedImports,
		Warnings:     warnings,
	}, nil
}

// rangePair is a [start, end) byte range into the working source.
type rangePair struct {
	start int
	end   int
}

// collectScopeRanges parses src and returns:
//   - excluded: ranges of comments + STRING basic literals (for scope=code_only)
//   - idents: ranges of every *ast.Ident (for scope=identifiers_only)
//
// Both slices are sorted by start offset.
func collectScopeRanges(filePath, src string) (excluded, idents []rangePair, err error) {
	fset := token.NewFileSet()
	f, perr := parser.ParseFile(fset, filePath, src, parser.ParseComments)
	if perr != nil {
		return nil, nil, perr
	}
	// Comments (both free-standing groups and attached doc comments end up
	// inside f.Comments, so iterating that covers everything).
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			start := fset.Position(c.Pos()).Offset
			end := fset.Position(c.End()).Offset
			excluded = append(excluded, rangePair{start: start, end: end})
		}
	}
	// String literals + identifiers. Visit every node.
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				start := fset.Position(x.Pos()).Offset
				end := fset.Position(x.End()).Offset
				excluded = append(excluded, rangePair{start: start, end: end})
			}
		case *ast.Ident:
			start := fset.Position(x.Pos()).Offset
			end := start + len(x.Name)
			idents = append(idents, rangePair{start: start, end: end})
		}
		return true
	})
	sort.Slice(excluded, func(i, j int) bool { return excluded[i].start < excluded[j].start })
	sort.Slice(idents, func(i, j int) bool { return idents[i].start < idents[j].start })
	return excluded, idents, nil
}

// rangeAllowed reports whether a match spanning [start,end) is permitted
// under the given scope.
//   - scope=code_only: reject the match if ANY byte of [start,end) falls inside
//     one of the excluded ranges (comment or string literal).
//   - scope=identifiers_only: accept only when [start,end) matches an identifier
//     range exactly (same start, same length).
//
// _ = src is unused today but kept in the signature for future diagnostics.
func rangeAllowed(start, end int, _ string, scope string, excluded, idents []rangePair) bool {
	switch scope {
	case "code_only":
		for _, r := range excluded {
			if r.start >= end {
				break
			}
			if r.end <= start {
				continue
			}
			// Any overlap disqualifies the match.
			return false
		}
		return true
	case "identifiers_only":
		// Binary search would be faster, but N is tiny. Linear is clearer.
		for _, r := range idents {
			if r.start == start && r.end == end {
				return true
			}
			if r.start > start {
				break
			}
		}
		return false
	default:
		return true
	}
}

// applyRangeReplacements rewrites src by replacing each range in ranges with
// the corresponding string in replaces. Ranges are assumed non-overlapping and
// already in ascending start order (which our caller produces).
func applyRangeReplacements(src string, ranges [][2]int, replaces []string) string {
	if len(ranges) == 0 {
		return src
	}
	var b strings.Builder
	// Rough capacity hint.
	b.Grow(len(src))
	prev := 0
	for i, r := range ranges {
		b.WriteString(src[prev:r[0]])
		b.WriteString(replaces[i])
		prev = r[1]
	}
	b.WriteString(src[prev:])
	return b.String()
}

// formatFilteredLines turns a list of filtered byte ranges into a short
// "L12, L34, L56" string for warnings. Line numbers are 1-based, computed from
// the given (pre-replacement) source.
func formatFilteredLines(src string, ranges [][2]int) string {
	if len(ranges) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ranges))
	seen := make(map[int]struct{}, len(ranges))
	for _, r := range ranges {
		line := 1 + strings.Count(src[:r[0]], "\n")
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		parts = append(parts, fmt.Sprintf("L%d", line))
	}
	return strings.Join(parts, ", ")
}
