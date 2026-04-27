// validateNoDroppedDecls and validateNoDroppedStmts are the issue #14 guard
// against silent data loss in op=replace patches whose replacement text is
// SHORTER (in bytes) than the matched text. The known failure mode is a
// multi-line shrinking replace that leaves the resulting file syntactically
// valid yet missing one or more declarations the agent meant to insert: the
// match consumed N declarations/statements, the replacement only re-inserted
// M < N, and because the result still parses go/format is happy.
//
// validateReplaceApplied (issue #3) catches the related case where the
// replacement substring is entirely absent. It does NOT catch the partial
// case where, say, the splice produced "type KMS = crypto.KMS" but also
// silently swallowed the surrounding decls. These helpers parse the
// replacement source plus the resulting source, build the set of top-level
// declaration names contributed by the replacement, and refuse the write
// when any of those names fails to land in the result.
//
// The check is intentionally conservative: only multi-line replacements with
// at least one top-level declaration are checked. Single-line replacements
// (e.g. "foo()" -> "bar()") are exempt because they cannot drop a whole
// declaration and ruling them out would block the common-case rename.
package commands

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// droppedContentCode is the structured error code returned when a patch
// would silently drop content. Distinct from PATCH_PRODUCES_INVALID_GO
// (which means "result fails to parse") and from PATCH_REPLACE_NOT_APPLIED
// (which means "the replacement substring is missing entirely").
const droppedContentCode = "PATCH_DROPPED_CONTENT"

// validateNoDroppedDecls checks that every top-level declaration name parsed
// from `replacement` is present as a top-level declaration in `postSrc`.
// Single-line replacements are skipped — only multi-line replacements that
// claim to introduce ≥1 named top-level declaration are validated.
//
// Returns nil when the check passes (or is not applicable). Returns a
// PATCH_DROPPED_CONTENT domain error when one or more replacement
// declarations are missing from the post-splice source — the caller is
// expected to roll back the write.
func validateNoDroppedDecls(filePath, replacement string, postSrc []byte) error {
	if !strings.Contains(replacement, "\n") {
		return nil
	}
	replNames, ok := parseReplacementDeclNames(replacement)
	if !ok || len(replNames) == 0 {
		return nil
	}
	postNames, ok := parseFileDeclNames(filePath, postSrc)
	if !ok {
		// If the post-source no longer parses, downstream validators (gofmt,
		// validateGoSource) will catch it — don't report DROPPED_CONTENT for
		// what is really a parse failure.
		return nil
	}
	missing := missingNames(replNames, postNames)
	if len(missing) == 0 {
		return nil
	}
	return &domain.Error{
		Code:    droppedContentCode,
		Message: formatDroppedDeclsMessage(missing, replacement),
	}
}

// validateNoDroppedStmts is the function-body counterpart of
// validateNoDroppedDecls. It compares the statement count of the post-splice
// function body to the count expected from `pre - matched + replacement`.
// When the post body has fewer statements than that, statements were
// dropped and the write is refused.
//
// Single-statement replacements (or empty/non-multiline replacements) are
// skipped — the check is meant to catch multi-statement shrinking replaces
// only.
//
// Returns nil when the check passes (or is not applicable), or a
// PATCH_DROPPED_CONTENT domain error when statements were dropped.
func validateNoDroppedStmts(replacement, matched, preBody, postBody string) error {
	if !strings.Contains(replacement, "\n") {
		return nil
	}
	replCount, ok := parseStmtCount(replacement)
	if !ok || replCount < 2 {
		return nil
	}
	matchedCount, ok := parseStmtCount(matched)
	if !ok {
		// If the match doesn't parse as a statement list (e.g. it is an
		// expression or part of one), we cannot compute the expected delta —
		// skip the check and let validateReplaceApplied handle the substring
		// presence test.
		return nil
	}
	preCount, ok := parseStmtCount(preBody)
	if !ok {
		return nil
	}
	postCount, ok := parseStmtCount(postBody)
	if !ok {
		return nil
	}
	expected := preCount - matchedCount + replCount
	if postCount >= expected {
		return nil
	}
	return &domain.Error{
		Code: droppedContentCode,
		Message: fmt.Sprintf(
			"patch (replace): function body has %d top-level statements after splice but %d were expected (pre=%d, matched=%d, replacement=%d) — write rolled back to prevent silent data loss. "+
				"This is the multi-line shrinking-replace bug (issue #14): the match consumed more statements than the replacement re-inserted. "+
				"Use update object=func with the full new body for multi-line edits.",
			postCount, expected, preCount, matchedCount, replCount,
		),
	}
}

// parseReplacementDeclNames parses `replacement` as the body of a Go file
// (a synthetic `package _` is prepended) and returns the slice of
// top-level declaration names — function names, type names, and var/const
// names. Returns ok=false when the replacement does not parse as Go top-
// level decls, in which case the caller should skip the dropped-content
// check (the replacement was not a top-level construct anyway).
func parseReplacementDeclNames(replacement string) (names []string, ok bool) {
	src := "package _\n" + replacement
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "replacement.go", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, false
	}
	return collectDeclNames(f), true
}

// parseFileDeclNames parses `src` as a Go file and returns the slice of
// top-level declaration names. Returns ok=false on parse failure so the
// caller can defer to the standard parse validator for a better error.
func parseFileDeclNames(filePath string, src []byte) (names []string, ok bool) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, false
	}
	return collectDeclNames(f), true
}

// collectDeclNames walks the top-level decls of f and returns each named
// entity. For a single GenDecl with multiple specs (`var ( a int; b int )`)
// every name is collected. Anonymous decls (a blank identifier) and import
// specs are skipped.
func collectDeclNames(f *ast.File) []string {
	var names []string
	for _, d := range f.Decls {
		switch x := d.(type) {
		case *ast.FuncDecl:
			if x.Name != nil && x.Name.Name != "" && x.Name.Name != "_" {
				key := x.Name.Name
				if x.Recv != nil && len(x.Recv.List) > 0 {
					key = recvTypeName(x.Recv.List[0].Type) + "." + key
				}
				names = append(names, key)
			}
		case *ast.GenDecl:
			for _, spec := range x.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name != nil && s.Name.Name != "" && s.Name.Name != "_" {
						names = append(names, s.Name.Name)
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n != nil && n.Name != "" && n.Name != "_" {
							names = append(names, n.Name)
						}
					}
				}
			}
		}
	}
	return names
}

// recvTypeName extracts the bare receiver type name (no pointer star) from
// an *ast.FuncDecl.Recv expression so methods are keyed by "Type.Method".
func recvTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return recvTypeName(e.X)
	case *ast.IndexExpr:
		return recvTypeName(e.X)
	case *ast.IndexListExpr:
		return recvTypeName(e.X)
	}
	return ""
}

// missingNames returns the subset of `expected` that does NOT appear in
// `present`. Order is preserved from `expected` so error messages list
// the missing names in the order the agent wrote them.
func missingNames(expected, present []string) []string {
	if len(expected) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(present))
	for _, n := range present {
		set[n] = struct{}{}
	}
	var missing []string
	for _, n := range expected {
		if _, ok := set[n]; !ok {
			missing = append(missing, n)
		}
	}
	return missing
}

// formatDroppedDeclsMessage builds the agent-facing error text shown when
// one or more declarations from the replacement failed to land in the
// resulting file. The replacement preview is truncated so the message
// stays compact even on large inputs.
func formatDroppedDeclsMessage(missing []string, replacement string) string {
	preview := truncateReplacementPreview(replacement, 120)
	plural := "declaration"
	if len(missing) > 1 {
		plural = "declarations"
	}
	return fmt.Sprintf(
		"patch (replace): replacement %s %s missing from result file — write rolled back to prevent silent data loss. "+
			"Expected the result to contain %s but it does not. "+
			"This is the multi-line shrinking-replace bug (issue #14): the match swallowed surrounding code that the replacement did not re-insert. "+
			"Use update object=file with the full new file content for whole-file rewrites, or update object=func for single-function rewrites. "+
			"Replacement preview: %q",
		plural, strings.Join(missing, ", "), strings.Join(missing, ", "), preview,
	)
}

// parseStmtCount wraps `body` in a synthetic `func _() { ... }` and returns
// the count of top-level statements it parses to. Returns ok=false when
// the body does not parse as a statement list — callers should skip the
// dropped-content check in that case (the input was not a statement list
// anyway, e.g. it was an expression fragment).
func parseStmtCount(body string) (count int, ok bool) {
	src := "package _\nfunc _() {\n" + body + "\n}\n"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "stmt_count.go", src, parser.SkipObjectResolution)
	if err != nil {
		return 0, false
	}
	if len(f.Decls) == 0 {
		return 0, false
	}
	fn, fnOk := f.Decls[0].(*ast.FuncDecl)
	if !fnOk || fn.Body == nil {
		return 0, false
	}
	return len(fn.Body.List), true
}
