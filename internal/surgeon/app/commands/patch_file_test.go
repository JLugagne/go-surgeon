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
