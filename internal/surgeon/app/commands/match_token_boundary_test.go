package commands_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Backlog item 1: a normalized substring match must never bind inside an
// identifier or numeric literal ("x = 1" must not match inside "max = 12").
// Backlog item 13: every same-line occurrence must be recorded so occurrence
// counting, ambiguity errors and occurrence:-1 are exact.

// --- item 1: partial-token binds ---

func TestPatchFunction_NormalizedMatch_RejectsPartialTokenBind(t *testing.T) {
	const path = "/tmp/token_boundary_fn.go"
	const content = "package p\n\nfunc F() {\n\tmax := 12\n\t_ = max\n}\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "F",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, Match: "x := 1", Replace: "x := 99"},
		},
	})
	require.Error(t, err, "match %q must not bind inside identifier 'max' / literal '12'", "x := 1")
	assert.Contains(t, err.Error(), "no match", "expected a no-match error, got: %v", err)
	assert.Equal(t, content, string(fs.files[path]), "file must be untouched")
}

func TestPatchDecl_NormalizedMatch_RejectsPartialTokenBind(t *testing.T) {
	const path = "/tmp/token_boundary_decl.go"
	const content = "package p\n\nvar limit = maxRetries * 12\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchDecl(context.Background(), domain.PatchDeclRequest{
		FilePath:   path,
		Identifier: "limit",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, Match: "s * 1", Replace: "s * 9"},
		},
	})
	require.Error(t, err, "match %q must not bind inside 'maxRetries' / '12'", "s * 1")
	assert.Contains(t, err.Error(), "no match", "expected a no-match error, got: %v", err)
	assert.Equal(t, content, string(fs.files[path]), "file must be untouched")
}

func TestPatchFile_NormalizedMode_RejectsPartialTokenBind(t *testing.T) {
	const path = "/tmp/token_boundary_file.go"
	const content = "package p\n\nfunc F() {\n\tmax := 12\n\t_ = max\n}\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	res, err := h.PatchFile(context.Background(), domain.PatchFileRequest{
		FilePath: path,
		Patches: []domain.FilePatch{
			{Match: "x := 1", Replace: "x := 99", MatchMode: "normalized"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 0, res.Hits[0], "partial-token bind must count as zero matches")
	assert.Equal(t, content, string(fs.files[path]), "file must be untouched")
}

// --- item 13: same-line duplicate occurrences ---

func TestPatchFunction_NormalizedMatch_OccurrenceAll_ReplacesSameLineDuplicates(t *testing.T) {
	const path = "/tmp/same_line_all_fn.go"
	const content = "package p\n\nfunc F() {\n\tuse(one, one)\n}\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "F",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, Match: "one", Replace: "two", Occurrence: -1},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, string(fs.files[path]), "use(two, two)",
		"occurrence:-1 must replace BOTH same-line hits")
}

func TestPatchFunction_NormalizedMatch_SameLineDuplicates_ErrorsWhenAmbiguous(t *testing.T) {
	const path = "/tmp/same_line_ambiguous_fn.go"
	const content = "package p\n\nfunc F() {\n\tuse(one, one)\n}\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "F",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, Match: "one", Replace: "two"},
		},
	})
	require.Error(t, err, "two same-line hits with occurrence:0 must be ambiguous")
	assert.Contains(t, err.Error(), "matched 2 times")
	assert.Equal(t, content, string(fs.files[path]), "file must be untouched")
}

func TestPatchFunction_NormalizedMatch_OccurrenceTwo_SelectsSecondSameLineHit(t *testing.T) {
	const path = "/tmp/same_line_second_fn.go"
	const content = "package p\n\nfunc F() {\n\tuse(one, one)\n}\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "F",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, Match: "one", Replace: "two", Occurrence: 2},
		},
	})
	require.NoError(t, err, "occurrence:2 must resolve the second same-line hit")
	assert.Contains(t, string(fs.files[path]), "use(one, two)")
}

func TestPatchDecl_NormalizedMatch_OccurrenceAll_ReplacesSameLineDuplicates(t *testing.T) {
	const path = "/tmp/same_line_all_decl.go"
	const content = "package p\n\nvar pair = join(one, one)\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchDecl(context.Background(), domain.PatchDeclRequest{
		FilePath:   path,
		Identifier: "pair",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, Match: "one", Replace: "two", Occurrence: -1},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, string(fs.files[path]), "join(two, two)",
		"occurrence:-1 must replace BOTH same-line hits")
}

func TestPatchFile_NormalizedMode_ReplacesEverySameLineOccurrence(t *testing.T) {
	const path = "/tmp/same_line_all_file.go"
	const content = "package p\n\nfunc F() {\n\tuse(one, one)\n}\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	res, err := h.PatchFile(context.Background(), domain.PatchFileRequest{
		FilePath: path,
		Patches: []domain.FilePatch{
			{Match: "one", Replace: "two", MatchMode: "normalized"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Hits[0], "both same-line hits must be counted")
	assert.Contains(t, string(fs.files[path]), "use(two, two)")
}
