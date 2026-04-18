package commands_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── const string value: replace ──────────────────────────────────────────────

func TestPatchDecl_ConstStringReplace(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

const greeting = "hello world"
`)
	res, err := h.PatchDecl(ctx, domain.PatchDeclRequest{
		FilePath:   "f.go",
		Identifier: "greeting",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, Match: "hello", Replace: "hi"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Applied)
	out := getFile(fs, "f.go")
	assert.Contains(t, out, `"hi world"`)
	assert.NotContains(t, out, `"hello world"`)
}

// ── grouped const block: target one member ───────────────────────────────────

func TestPatchDecl_GroupedConstBlockMember(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

const (
	A = "alpha"
	B = "bravo"
	C = "charlie"
)
`)
	res, err := h.PatchDecl(ctx, domain.PatchDeclRequest{
		FilePath:   "f.go",
		Identifier: "B",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, Match: "bravo", Replace: "beta"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Applied)
	out := getFile(fs, "f.go")
	assert.Contains(t, out, `"alpha"`)
	assert.Contains(t, out, `"beta"`)
	assert.NotContains(t, out, `"bravo"`)
	assert.Contains(t, out, `"charlie"`)
}

// ── var with initializer ─────────────────────────────────────────────────────

func TestPatchDecl_VarWithInitializer(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

var defaultURL = "https://example.com/api"
`)
	res, err := h.PatchDecl(ctx, domain.PatchDeclRequest{
		FilePath:   "f.go",
		Identifier: "defaultURL",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, Match: "example.com", Replace: "prod.example.com"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Applied)
	assert.Contains(t, getFile(fs, "f.go"), `"https://prod.example.com/api"`)
}

// ── typed var without initializer → NODE_NOT_FOUND ───────────────────────────

func TestPatchDecl_TypedVarNoInitializer(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

var counter int
`)
	_, err := h.PatchDecl(ctx, domain.PatchDeclRequest{
		FilePath:   "f.go",
		Identifier: "counter",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, Match: "int", Replace: "int64"},
		},
	})
	require.Error(t, err)
	var de *domain.Error
	require.ErrorAs(t, err, &de)
	assert.Equal(t, "NODE_NOT_FOUND", de.Code)
	assert.Contains(t, de.Message, "no value expression")
}

// ── multi-line raw-string literal: insert_after ──────────────────────────────

func TestPatchDecl_MultilineRawStringInsertAfter(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", "package p\n\nconst instructions = `line 1\nline 2\nline 3`\n")
	res, err := h.PatchDecl(ctx, domain.PatchDeclRequest{
		FilePath:   "f.go",
		Identifier: "instructions",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpInsertAfter, Match: "line 2", Code: "line 2.5"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Applied)
	out := getFile(fs, "f.go")
	// Should contain all four lines in order, inside the backticks.
	idx1 := strings.Index(out, "line 1")
	idx2 := strings.Index(out, "line 2\n")
	idx25 := strings.Index(out, "line 2.5")
	idx3 := strings.Index(out, "line 3")
	require.True(t, idx1 >= 0 && idx2 > idx1 && idx25 > idx2 && idx3 > idx25,
		"expected ordered lines 1 < 2 < 2.5 < 3 in output\n%s", out)
	// Backticks should be preserved as delimiters (not touched).
	assert.Equal(t, 2, strings.Count(out, "`"))
}

// ── invalid identifier ───────────────────────────────────────────────────────

func TestPatchDecl_InvalidIdentifier(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

const greeting = "hi"
`)
	_, err := h.PatchDecl(ctx, domain.PatchDeclRequest{
		FilePath:   "f.go",
		Identifier: "doesNotExist",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, Match: "hi", Replace: "hello"},
		},
	})
	require.Error(t, err)
	var de *domain.Error
	require.ErrorAs(t, err, &de)
	assert.Equal(t, "NODE_NOT_FOUND", de.Code)
	assert.Contains(t, de.Message, "doesNotExist")
}

// ── preview mode ─────────────────────────────────────────────────────────────

func TestPatchDecl_PreviewDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	original := `package p

const greeting = "hello world"
`
	setFile(fs, "f.go", original)
	res, err := h.PatchDecl(ctx, domain.PatchDeclRequest{
		FilePath:   "f.go",
		Identifier: "greeting",
		Preview:    true,
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, Match: "hello", Replace: "hi"},
		},
	})
	require.NoError(t, err)
	assert.True(t, res.Preview)
	assert.NotEmpty(t, res.Diff)
	// File on disk is unchanged.
	assert.Equal(t, original, getFile(fs, "f.go"))
}

// ── occurrence disambiguation ────────────────────────────────────────────────

func TestPatchDecl_OccurrenceDisambiguation(t *testing.T) {
	ctx := context.Background()

	t.Run("ambiguous without occurrence fails with candidates", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", "package p\n\nconst banner = `foo\nfoo\nfoo`\n")
		_, err := h.PatchDecl(ctx, domain.PatchDeclRequest{
			FilePath:   "f.go",
			Identifier: "banner",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "foo", Replace: "bar"},
			},
		})
		require.Error(t, err)
		var de *domain.Error
		require.ErrorAs(t, err, &de)
		assert.Equal(t, "PATCH_FAILED", de.Code)
		assert.Contains(t, de.Message, "matched 3 times")
		assert.Contains(t, de.Message, "Disambiguate with occurrence")
		// Candidates now include absolute line numbers plus an at_line retry hint.
		assert.Regexp(t, `L\d+: foo`, de.Message)
		assert.Contains(t, de.Message, "Hint: retry with at_line:")
		assert.Contains(t, de.Message, "or use occurrence: 1..3")
	})

	t.Run("occurrence=2 picks the second match and warns about leftovers", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", "package p\n\nconst banner = `foo\nfoo\nfoo`\n")
		res, err := h.PatchDecl(ctx, domain.PatchDeclRequest{
			FilePath:   "f.go",
			Identifier: "banner",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "foo", Replace: "bar", Occurrence: 2},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, res.Applied)
		out := getFile(fs, "f.go")
		// Only the second occurrence should be replaced.
		assert.Equal(t, 1, strings.Count(out, "bar"))
		assert.Equal(t, 2, strings.Count(out, "foo"))
		require.NotEmpty(t, res.Warnings, "expected leftover warning")
		assert.Contains(t, res.Warnings[0], "2 more match(es) remain")
	})
}

// ── line-mode targeting ──────────────────────────────────────────────────────

func TestPatchDecl_LineModeReplace(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	// The raw-string value starts on line 3. Inside the content the first
	// content line is "line 1" at file line 3 (because the backtick opens
	// the string literal on line 3 too). For backtick content, ADR 0006
	// says body starts at the byte after the opening backtick — so
	// bodyStartLine is the line of the backtick itself.
	setFile(fs, "f.go", "package p\n\nconst instructions = `line 1\nline 2\nline 3`\n")
	// Replace file line 4, which is "line 2" inside the backticked content.
	res, err := h.PatchDecl(ctx, domain.PatchDeclRequest{
		FilePath:   "f.go",
		Identifier: "instructions",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, AtLine: 4, Replace: "LINE TWO"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Applied)
	out := getFile(fs, "f.go")
	assert.Contains(t, out, "LINE TWO")
	assert.NotContains(t, out, "line 2")
	// Lines 1 and 3 untouched.
	assert.Contains(t, out, "line 1")
	assert.Contains(t, out, "line 3")
}

// ── non-string value: full-expression targeting ──────────────────────────────

func TestPatchDecl_NonStringValue(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

var defaults = []int{1, 2, 3}
`)
	// Non-string value → origBody is the full expression "[]int{1, 2, 3}".
	// We can match on the composite literal interior.
	res, err := h.PatchDecl(ctx, domain.PatchDeclRequest{
		FilePath:   "f.go",
		Identifier: "defaults",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, Match: "1, 2, 3", Replace: "10, 20, 30"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Applied)
	assert.Contains(t, getFile(fs, "f.go"), "[]int{10, 20, 30}")
}

// ── error var (domain.Error-like value) ──────────────────────────────────────

func TestPatchDecl_ErrorVar(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

import "errors"

var ErrNotFound = errors.New("not found")
`)
	res, err := h.PatchDecl(ctx, domain.PatchDeclRequest{
		FilePath:   "f.go",
		Identifier: "ErrNotFound",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, Match: "not found", Replace: "resource not found"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Applied)
	// Non-string BasicLit (it's a call expression), so origBody is the full
	// expression errors.New("not found"). The substring "not found" appears
	// inside the string arg, which is matched by findNormalizedMatches.
	assert.Contains(t, getFile(fs, "f.go"), `errors.New("resource not found")`)
}

// ── patch produces invalid Go is rejected before writing ─────────────────────

func TestPatchDecl_InvalidGoRejected(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	original := `package p

var defaults = []int{1, 2, 3}
`
	setFile(fs, "f.go", original)
	_, err := h.PatchDecl(ctx, domain.PatchDeclRequest{
		FilePath:   "f.go",
		Identifier: "defaults",
		Patches: []domain.FunctionPatch{
			// This replace turns the initializer into syntactically broken Go:
			// `]int{1, 2, 3}` has an extra ']' before `int`.
			{Op: domain.PatchOpReplace, Match: "[]int{1, 2, 3}", Replace: "]int{1, 2, 3}"},
		},
	})
	require.Error(t, err)
	var de *domain.Error
	require.ErrorAs(t, err, &de)
	assert.Equal(t, "PATCH_PRODUCES_INVALID_GO", de.Code)
	// File untouched.
	assert.Equal(t, original, getFile(fs, "f.go"))
}
