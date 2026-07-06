package commands_test

// Backlog items 2 and 3 (match-engine group):
//   2. Token-fallback hits (FindTokenMatches) were extended to full-line
//      boundaries, so replace/delete on a sub-expression consumed the entire
//      line instead of only the matched range.
//   3. wrap on a partial-line match replaced the whole line, silently dropping
//      the code before/after the matched fragment (patch function AND decl).

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── item 2: token-fallback sub-line hits ──────────────────────────────────────

func TestFindTokenMatches_SubLineHitKeepsExactRange(t *testing.T) {
	body := "\t_, _ = foo(a), bar(b)\n"
	// Whitespace divergence forces the token stage; the hit must span only
	// the matched sub-expression, not the whole line.
	hits := commands.FindTokenMatches(body, "foo( a )")
	require.Len(t, hits, 1)
	assert.Equal(t, "foo(a)", body[hits[0][0]:hits[0][1]])
}

func TestFindTokenMatches_WholeLineHitStillExtendsToLineBounds(t *testing.T) {
	body := "\tx := 1\n"
	hits := commands.FindTokenMatches(body, "x := 1")
	require.Len(t, hits, 1)
	assert.Equal(t, "\tx := 1", body[hits[0][0]:hits[0][1]])
}

func TestPatchFunction_TokenFallbackReplace_SubExpressionKeepsRestOfLine(t *testing.T) {
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

func F() {
	_, _ = foo(a), bar(b)
}
`)
	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "f.go",
		Identifier: "F",
		Patches: []domain.FunctionPatch{{
			Op:      domain.PatchOpReplace,
			Match:   "foo( a )", // spaces inside parens force the token fallback
			Replace: "foo(a2)",
		}},
	})
	require.NoError(t, err)
	got := getFile(fs, "f.go")
	assert.Contains(t, got, "_, _ = foo(a2), bar(b)",
		"replace must splice only the matched sub-expression, not the whole line")
}

func TestPatchFunction_TokenFallbackDelete_SubExpressionKeepsRestOfLine(t *testing.T) {
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

func G() {
	a(); b()
	c()
}
`)
	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "f.go",
		Identifier: "G",
		Patches: []domain.FunctionPatch{{
			Op:    domain.PatchOpDelete,
			Match: "b( )", // spaces inside parens force the token fallback
		}},
	})
	require.NoError(t, err)
	got := getFile(fs, "f.go")
	assert.Contains(t, got, "a()", "delete of a sub-expression must keep the rest of the line")
	assert.NotContains(t, got, "b()")
}

func TestPatchFile_NormalizedTokenFallback_ReplaceKeepsRestOfLine(t *testing.T) {
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

func F() {
	_, _ = foo(a), bar(b)
}
`)
	_, err := h.PatchFile(context.Background(), domain.PatchFileRequest{
		FilePath: "f.go",
		Patches: []domain.FilePatch{{
			Match:     "foo( a )", // spaces inside parens force the token fallback
			MatchMode: "normalized",
			Replace:   "foo(a2)",
		}},
	})
	require.NoError(t, err)
	got := getFile(fs, "f.go")
	assert.Contains(t, got, "foo(a2)")
	assert.Contains(t, got, "bar(b)",
		"normalized-mode replace must not consume the rest of the line")
}

// ── item 3: wrap on partial-line matches ──────────────────────────────────────

func TestPatchFunction_Wrap_SubLineMatchWrapsOnlyMatchedFragment(t *testing.T) {
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

func H() {
	x := compute(); log(x)
}
`)
	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "f.go",
		Identifier: "H",
		Patches: []domain.FunctionPatch{{
			Op:    domain.PatchOpWrap,
			Match: "compute()",
			Wrap:  "retry(%s)",
		}},
	})
	require.NoError(t, err)
	got := getFile(fs, "f.go")
	assert.Contains(t, got, "x := retry(compute()); log(x)",
		"wrap of a sub-line match must keep the rest of the line")
}

func TestPatchFunction_Wrap_OccurrenceAllSubLineMatchesOnSameLine(t *testing.T) {
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

func K() {
	x := compute(); y := compute()
	use(x, y)
}
`)
	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "f.go",
		Identifier: "K",
		Patches: []domain.FunctionPatch{{
			Op:         domain.PatchOpWrap,
			Match:      "compute()",
			Occurrence: -1,
			Wrap:       "retry(%s)",
		}},
	})
	require.NoError(t, err)
	got := getFile(fs, "f.go")
	assert.Contains(t, got, "x := retry(compute()); y := retry(compute())",
		"occurrence:-1 wrap must wrap each sub-line match in place")
}

func TestPatchDecl_Wrap_SubLineMatchWrapsOnlyMatchedFragment(t *testing.T) {
	h, fs := newPatchHandler()
	setFile(fs, "d.go", `package p

var timeout = compute(5) + 3
`)
	_, err := h.PatchDecl(context.Background(), domain.PatchDeclRequest{
		FilePath:   "d.go",
		Identifier: "timeout",
		Patches: []domain.FunctionPatch{{
			Op:    domain.PatchOpWrap,
			Match: "compute(5)",
			Wrap:  "scale(%s)",
		}},
	})
	require.NoError(t, err)
	got := getFile(fs, "d.go")
	assert.Contains(t, got, "var timeout = scale(compute(5)) + 3",
		"wrap of a sub-expression in a decl value must keep the rest of the expression")
}

// Whole-line wrap keeps the legacy semantics: the full line is replaced by
// indent + wrapped statement (pinned so the fix does not regress it).
func TestPatchFunction_Wrap_WholeLineMatchKeepsLegacyBehavior(t *testing.T) {
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

func L() error {
	doFetch()
	return nil
}
`)
	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   "f.go",
		Identifier: "L",
		Patches: []domain.FunctionPatch{{
			Op:    domain.PatchOpWrap,
			Match: "doFetch()",
			Wrap:  "if err := %s; err != nil { return err }",
		}},
	})
	require.NoError(t, err)
	got := getFile(fs, "f.go")
	assert.Contains(t, got, "\tif err := doFetch(); err != nil { return err }\n")
	assert.NotContains(t, got, "\tdoFetch()\n")
}
