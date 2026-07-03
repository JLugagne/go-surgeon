package commands_test

// Backlog items 2 and 3 (match-engine group):
//   2. Token-fallback hits (FindTokenMatches) must not be extended to full-line
//      boundaries when they only cover a sub-expression — replace/delete would
//      silently consume the rest of the line.
//   3. wrap on a partial-line match must splice wrap(matchedFragment) over the
//      matched range only, not replace the whole line.

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPatchHarness returns a handler over an in-memory FS holding src at path.
func newPatchHarness(src string) (*commands.ExecutePlanHandler, *mockFS, string) {
	const path = "/tmp/subline_match_target.go"
	fs := &mockFS{files: map[string][]byte{path: []byte(src)}}
	return commands.NewExecutePlanHandler(fs), fs, path
}

// TestPatchFunction_TokenFallbackReplace_SubLineKeepsRestOfLine exercises
// backlog item 2 (replace): the match "foo( a )" only resolves through the
// token-fallback stage (whitespace normalization cannot equate "foo( a )"
// and "foo(a)"), and the token span covers a sub-expression of the line.
// The replace must touch only that sub-expression.
func TestPatchFunction_TokenFallbackReplace_SubLineKeepsRestOfLine(t *testing.T) {
	src := `package p

func Target() {
	_, _ = foo(a), bar(b)
}
`
	h, fs, path := newPatchHarness(src)

	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "Target",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, Match: "foo( a )", Replace: "foo(a2)"},
		},
	})
	require.NoError(t, err)

	got := string(fs.files[path])
	assert.Contains(t, got, "_, _ = foo(a2), bar(b)",
		"replace on a sub-line token match must keep the rest of the line")
	assert.NotContains(t, got, "\tfoo(a2)\n",
		"the whole line must not be collapsed to the bare replacement")
}

// TestPatchFunction_TokenFallbackDelete_SubLineKeepsRestOfLine exercises
// backlog item 2 (delete): deleting a sub-expression matched via the token
// fallback must remove only the matched bytes, not the whole line.
func TestPatchFunction_TokenFallbackDelete_SubLineKeepsRestOfLine(t *testing.T) {
	src := `package p

func Target() {
	x := a + b
	use(x)
}
`
	h, fs, path := newPatchHarness(src)

	// "+b" only matches through the token stage (the body spells it "+ b").
	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "Target",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpDelete, Match: "+b"},
		},
	})
	require.NoError(t, err)

	got := string(fs.files[path])
	assert.Contains(t, got, "x := a", "the assignment must survive a sub-expression delete")
	assert.NotContains(t, got, "+ b", "the matched sub-expression must be gone")
}

// TestPatchFunction_TokenFallbackDelete_WholeLineStillExtends pins the
// legacy whole-line semantics: when the token span covers the entire line
// content, the delete still consumes the full line including its newline.
func TestPatchFunction_TokenFallbackDelete_WholeLineStillExtends(t *testing.T) {
	src := `package p

func Target() {
	doWork(  )
	done()
}
`
	h, fs, path := newPatchHarness(src)

	// "doWork()" vs "doWork(  )": token streams agree, normalized text does not.
	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "Target",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpDelete, Match: "doWork()"},
		},
	})
	require.NoError(t, err)

	got := string(fs.files[path])
	assert.NotContains(t, got, "doWork", "the whole-line match must be deleted")
	assert.Contains(t, got, "func Target() {\n\tdone()\n}", "no empty line must be left behind")
}

// TestPatchFunction_WrapPartialLine_PreservesRestOfLine exercises backlog
// item 3: wrap on a sub-line match must splice wrap(matchedFragment) over
// the matched range only. Historically it replaced the entire line with
// indent + wrap(match), silently dropping "x := " and "; log(x)".
func TestPatchFunction_WrapPartialLine_PreservesRestOfLine(t *testing.T) {
	src := `package p

func Target() {
	x := compute(); log(x)
}
`
	h, fs, path := newPatchHarness(src)

	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "Target",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpWrap, Match: "compute()", Wrap: "retry(%s)"},
		},
	})
	require.NoError(t, err)

	got := string(fs.files[path])
	assert.Contains(t, got, "x := retry(compute()); log(x)",
		"wrap on a sub-line match must keep the rest of the line")
	assert.NotContains(t, got, "\tretry(compute())\n",
		"the whole line must not be collapsed to the wrapped fragment")
}

// TestPatchFunction_WrapWholeLine_KeepsLegacySemantics pins the documented
// whole-line behavior: a match spanning the full line content is wrapped in
// place with the original indentation.
func TestPatchFunction_WrapWholeLine_KeepsLegacySemantics(t *testing.T) {
	src := `package p

func Target() {
	doWork()
}
`
	h, fs, path := newPatchHarness(src)

	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "Target",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpWrap, Match: "doWork()", Wrap: "go %s"},
		},
	})
	require.NoError(t, err)

	got := string(fs.files[path])
	assert.Contains(t, got, "\tgo doWork()\n", "whole-line wrap keeps indent and replaces the line")
}

// TestPatchDecl_WrapPartialValue_PreservesRest exercises backlog item 3 on
// the decl surface: wrapping a sub-expression of a var value must keep the
// remainder of the value expression.
func TestPatchDecl_WrapPartialValue_PreservesRest(t *testing.T) {
	src := `package p

var timeout = base * 2

var base = 21
`
	h, fs, path := newPatchHarness(src)

	_, err := h.PatchDecl(context.Background(), domain.PatchDeclRequest{
		FilePath:   path,
		Identifier: "timeout",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpWrap, Match: "base", Wrap: "clamp(%s)"},
		},
	})
	require.NoError(t, err)

	got := string(fs.files[path])
	assert.Contains(t, got, "var timeout = clamp(base) * 2",
		"wrap on a sub-expression of a decl value must keep the rest of the expression")
}

// TestPatchDecl_TokenFallbackDelete_SubExpressionOnly exercises backlog
// item 2 on the decl surface via the shared token-fallback stage.
func TestPatchDecl_TokenFallbackDelete_SubExpressionOnly(t *testing.T) {
	src := `package p

var total = a + b

var a, b = 1, 2
`
	h, fs, path := newPatchHarness(src)

	_, err := h.PatchDecl(context.Background(), domain.PatchDeclRequest{
		FilePath:   path,
		Identifier: "total",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpDelete, Match: "+b"},
		},
	})
	require.NoError(t, err)

	got := string(fs.files[path])
	assert.Contains(t, got, "var total = a", "the head of the value expression must survive")
	assert.NotContains(t, got, "+ b", "the matched sub-expression must be gone")
}

// TestPatchFile_NormalizedTokenFallback_SubLineReplace exercises backlog
// item 2 through patch_file match_mode=normalized, which shares
// findNormalizedMatches (and therefore the token fallback) with the
// function/decl engines.
func TestPatchFile_NormalizedTokenFallback_SubLineReplace(t *testing.T) {
	src := `package p

func Target() {
	_, _ = foo(a), bar(b)
}
`
	h, fs, path := newPatchHarness(src)

	_, err := h.PatchFile(context.Background(), domain.PatchFileRequest{
		FilePath: path,
		Patches: []domain.FilePatch{
			{Match: "foo( a )", MatchMode: "normalized", Replace: "foo(a2)"},
		},
	})
	require.NoError(t, err)

	got := string(fs.files[path])
	assert.Contains(t, got, "_, _ = foo(a2), bar(b)",
		"normalized token-fallback replace must keep the rest of the line")
}
