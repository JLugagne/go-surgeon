package commands_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPatchFile_LiteralMultiOccurrence: the headline case that motivates the
// tool — rename a literal that appears many times across several functions.
func TestPatchFile_LiteralMultiOccurrence(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

func TestA() string { return "graph" }
func TestB() string { return "graph-b" }
func TestC() string {
	x := "graph"
	y := "graph"
	return x + y
}
`)
	res, err := h.PatchFile(ctx, domain.PatchFileRequest{
		FilePath: "f.go",
		Patches: []domain.FilePatch{
			{Match: `"graph"`, Replace: `"overview"`},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Applied)
	require.Len(t, res.Hits, 1)
	assert.Equal(t, 3, res.Hits[0], "literal must replace all 3 occurrences")
	assert.Empty(t, res.Warnings)

	got := getFile(fs, "f.go")
	assert.Equal(t, 3, strings.Count(got, `"overview"`))
	assert.NotContains(t, got, `"graph"`)
	// "graph-b" must stay untouched — the literal match is byte-exact.
	assert.Contains(t, got, `"graph-b"`)
}

// TestPatchFile_RegexSubmatch: $1/$2 backref substitution via RE2.
func TestPatchFile_RegexSubmatch(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

func OldNameFoo() int { return 1 }
func OldNameBar() int { return 2 }
func OtherFn() int    { return 3 }
`)
	res, err := h.PatchFile(ctx, domain.PatchFileRequest{
		FilePath: "f.go",
		Patches: []domain.FilePatch{
			{MatchRegex: `OldName(\w+)`, Replace: `NewName$1`},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Applied)
	assert.Equal(t, 2, res.Hits[0])

	got := getFile(fs, "f.go")
	assert.Contains(t, got, "NewNameFoo")
	assert.Contains(t, got, "NewNameBar")
	assert.NotContains(t, got, "OldName")
	assert.Contains(t, got, "OtherFn")
}

// TestPatchFile_Preview: preview=true returns a diff without writing.
func TestPatchFile_Preview(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	original := `package p
func F() string { return "graph" }
`
	setFile(fs, "f.go", original)

	res, err := h.PatchFile(ctx, domain.PatchFileRequest{
		FilePath: "f.go",
		Preview:  true,
		Patches: []domain.FilePatch{
			{Match: `"graph"`, Replace: `"overview"`},
		},
	})
	require.NoError(t, err)
	assert.True(t, res.Preview)
	assert.NotEmpty(t, res.Diff)
	assert.Contains(t, res.Diff, `-`)
	assert.Contains(t, res.Diff, `+`)
	// File on disk must be unchanged.
	assert.Equal(t, original, getFile(fs, "f.go"))
}

// TestPatchFile_ParseFailureRollback: if the substitution produces invalid
// Go, nothing is written and the file on disk is unchanged.
func TestPatchFile_ParseFailureRollback(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	original := `package p
func F() int { return 1 }
`
	setFile(fs, "f.go", original)

	// Replace "func" with "fnc" — unmistakable syntax break.
	_, err := h.PatchFile(ctx, domain.PatchFileRequest{
		FilePath: "f.go",
		Patches: []domain.FilePatch{
			{Match: "func F()", Replace: "fnc F()"},
		},
	})
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	assert.Equal(t, "PATCH_PRODUCES_INVALID_GO", domErr.Code)

	// File must be untouched.
	assert.Equal(t, original, getFile(fs, "f.go"))
}

// TestPatchFile_ZeroMatchWarning: patches with no matches are recorded as
// warnings, not errors, so "defensive rename" patches are allowed.
func TestPatchFile_ZeroMatchWarning(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p
func F() int { return 1 }
`)
	res, err := h.PatchFile(ctx, domain.PatchFileRequest{
		FilePath: "f.go",
		Patches: []domain.FilePatch{
			{Match: "nonexistent-token", Replace: "replacement"},
			{Match: "return 1", Replace: "return 2"}, // this one does match
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Applied)
	require.Len(t, res.Hits, 2)
	assert.Equal(t, 0, res.Hits[0])
	assert.Equal(t, 1, res.Hits[1])
	require.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "patch #1")
	assert.Contains(t, res.Warnings[0], "zero matches")

	got := getFile(fs, "f.go")
	assert.Contains(t, got, "return 2")
}

// TestPatchFile_EmptyPatchesRejected: an empty patches list is a hard error.
func TestPatchFile_EmptyPatchesRejected(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p
func F() {}
`)
	_, err := h.PatchFile(ctx, domain.PatchFileRequest{
		FilePath: "f.go",
		Patches:  []domain.FilePatch{},
	})
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	assert.Equal(t, "INVALID_ARGUMENT", domErr.Code)
}

// TestPatchFile_NonGoFileRejected: the handler defends against misuse from
// callers that didn't pre-validate the file extension.
func TestPatchFile_NonGoFileRejected(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "README.md", "# Title")
	_, err := h.PatchFile(ctx, domain.PatchFileRequest{
		FilePath: "README.md",
		Patches: []domain.FilePatch{
			{Match: "Title", Replace: "Heading"},
		},
	})
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	assert.Equal(t, "INVALID_ARGUMENT", domErr.Code)
	assert.Contains(t, domErr.Message, ".go")
}

// TestPatchFile_SequentialApplication: each patch sees the result of the
// previous one (not the original body).
func TestPatchFile_SequentialApplication(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p
func F() string { return "a" }
`)
	res, err := h.PatchFile(ctx, domain.PatchFileRequest{
		FilePath: "f.go",
		Patches: []domain.FilePatch{
			{Match: `"a"`, Replace: `"b"`}, // turns "a" -> "b"
			{Match: `"b"`, Replace: `"c"`}, // then "b" -> "c"
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Hits[0])
	assert.Equal(t, 1, res.Hits[1])
	assert.Contains(t, getFile(fs, "f.go"), `"c"`)
}

// TestPatchFile_MutuallyExclusiveMatch: a patch with both match and
// match_regex set is a hard error.
func TestPatchFile_MutuallyExclusiveMatch(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p
func F() {}
`)
	_, err := h.PatchFile(ctx, domain.PatchFileRequest{
		FilePath: "f.go",
		Patches: []domain.FilePatch{
			{Match: "F", MatchRegex: "F", Replace: "G"},
		},
	})
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	assert.Equal(t, "PATCH_FAILED", domErr.Code)
	assert.Contains(t, domErr.Message, "mutually exclusive")
}

// TestPatchFile_CodeOnly_SkipsCommentAndString: scope=code_only must leave
// comments and string literals untouched, while still rewriting the identifier
// occurrences.
func TestPatchFile_CodeOnly_SkipsCommentAndString(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

// TODO: rename foo here
var s = "foo-bar"

func foo() {}

var g = foo
`)
	res, err := h.PatchFile(ctx, domain.PatchFileRequest{
		FilePath: "f.go",
		Scope:    "code_only",
		Patches: []domain.FilePatch{
			{Match: "foo", Replace: "bar"},
		},
	})
	require.NoError(t, err)
	require.Len(t, res.Hits, 1)
	// 2 accepted (func decl + var assign); 2 filtered (comment + inside string).
	assert.Equal(t, 2, res.Hits[0])

	got := getFile(fs, "f.go")
	// Comment untouched.
	assert.Contains(t, got, "// TODO: rename foo here")
	// String untouched.
	assert.Contains(t, got, `"foo-bar"`)
	// Identifier rewrites happened.
	assert.Contains(t, got, "func bar()")
	assert.Contains(t, got, "var g = bar")
	// The original `func foo` must be gone.
	assert.NotContains(t, got, "func foo()")
}

// TestPatchFile_IdentifiersOnly_SkipsSubstrings: scope=identifiers_only must
// only accept matches that land exactly on an *ast.Ident boundary — so a
// substring inside a longer identifier (fooBar) is NOT rewritten, and neither
// are strings/comments.
func TestPatchFile_IdentifiersOnly_SkipsSubstrings(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

// foo in a comment
var fooBar = 1
var foo = 2
var s = "foo"
`)
	res, err := h.PatchFile(ctx, domain.PatchFileRequest{
		FilePath: "f.go",
		Scope:    "identifiers_only",
		Patches: []domain.FilePatch{
			{Match: "foo", Replace: "baz"},
		},
	})
	require.NoError(t, err)
	require.Len(t, res.Hits, 1)
	// Only the standalone `foo` ident qualifies. fooBar is a longer ident (rejected),
	// "foo" is inside a STRING literal (BasicLit, not Ident — rejected), "// foo" is a
	// comment (rejected).
	assert.Equal(t, 1, res.Hits[0])

	got := getFile(fs, "f.go")
	// Standalone ident was rewritten.
	assert.Contains(t, got, "var baz = 2")
	// Longer identifier preserved.
	assert.Contains(t, got, "var fooBar = 1")
	// String preserved.
	assert.Contains(t, got, `"foo"`)
	// Comment preserved.
	assert.Contains(t, got, "// foo in a comment")
}

// TestPatchFile_InvalidScope_Rejected: an unknown scope value must be rejected
// with INVALID_ARGUMENT rather than silently falling through.
func TestPatchFile_InvalidScope_Rejected(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", "package p\n\nfunc F() {}\n")
	_, err := h.PatchFile(ctx, domain.PatchFileRequest{
		FilePath: "f.go",
		Scope:    "weird",
		Patches: []domain.FilePatch{
			{Match: "F", Replace: "G"},
		},
	})
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	assert.Equal(t, "INVALID_ARGUMENT", domErr.Code)
	assert.Contains(t, domErr.Message, "scope must be")
}

// TestPatchFile_ScopeAll_PreservesCurrentBehavior: with scope="all" (or empty)
// the tool replaces every occurrence — including inside comments and strings
// — just like before the scope feature existed.
func TestPatchFile_ScopeAll_PreservesCurrentBehavior(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	src := `package p

// foo in a comment
var s = "foo"
var foo = 1
`
	setFile(fs, "f.go", src)
	res, err := h.PatchFile(ctx, domain.PatchFileRequest{
		FilePath: "f.go",
		Scope:    "all",
		Patches: []domain.FilePatch{
			{Match: "foo", Replace: "bar"},
		},
	})
	require.NoError(t, err)
	require.Len(t, res.Hits, 1)
	// 3 occurrences: comment + string + ident. All rewritten under scope=all.
	assert.Equal(t, 3, res.Hits[0])
	assert.Empty(t, res.Warnings)

	got := getFile(fs, "f.go")
	assert.Contains(t, got, "// bar in a comment")
	assert.Contains(t, got, `"bar"`)
	assert.Contains(t, got, "var bar = 1")
	assert.Equal(t, 0, strings.Count(got, "foo"))
}

// TestPatchFile_CodeOnly_ZeroEffectiveMatchesWarning: when every match falls
// inside an excluded range (all in comments), hits=0 and a warning is emitted
// — but no error, and the file is unchanged.
func TestPatchFile_CodeOnly_ZeroEffectiveMatchesWarning(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	original := `package p

// foo line one
// foo line two

func F() {}
`
	setFile(fs, "f.go", original)
	res, err := h.PatchFile(ctx, domain.PatchFileRequest{
		FilePath: "f.go",
		Scope:    "code_only",
		Patches: []domain.FilePatch{
			{Match: "foo", Replace: "bar"},
		},
	})
	require.NoError(t, err)
	require.Len(t, res.Hits, 1)
	assert.Equal(t, 0, res.Hits[0], "every match filtered out -> hits=0")
	require.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "patch #1")
	assert.Contains(t, res.Warnings[0], "filtered out by scope=code_only")

	got := getFile(fs, "f.go")
	// File must be unchanged (gofmt may normalize trailing whitespace but the
	// key "foo" / "bar" assertions survive any of that).
	assert.Contains(t, got, "// foo line one")
	assert.Contains(t, got, "// foo line two")
	assert.NotContains(t, got, "bar")
}
