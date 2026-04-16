package commands

import (
	"context"
	"fmt"
	"go/format"
	"regexp"
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
		if p.MatchRegex != "" {
			re, regErr := regexp.Compile(p.MatchRegex)
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
		count := 0
		if p.MatchRegex != "" {
			re := compiled[i]
			matches := re.FindAllStringIndex(working, -1)
			count = len(matches)
			if count > 0 {
				working = re.ReplaceAllString(working, p.Replace)
			}
		} else {
			count = strings.Count(working, p.Match)
			if count > 0 {
				working = strings.ReplaceAll(working, p.Match, p.Replace)
			}
		}
		hits[i] = count
		if count == 0 {
			needle := p.Match
			if needle == "" {
				needle = p.MatchRegex
			}
			warnings = append(warnings, fmt.Sprintf("patch #%d: zero matches for %q — no changes from this patch", i+1, needle))
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
