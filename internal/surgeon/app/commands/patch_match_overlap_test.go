package commands_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPatchFile_NormalizedPeriodicMultilineMatch_NoOverlapPanic reproduces
// backlog item 4: periodic content ("1,\n1,\n1,") makes the normalized
// multi-line stage emit OVERLAPPING ranges (it advances the search cursor by
// one normalized byte instead of past the accepted match), and
// applyRangeReplacements slice-panics on the second range. Matches must be
// non-overlapping: exactly one hit here.
func TestPatchFile_NormalizedPeriodicMultilineMatch_NoOverlapPanic(t *testing.T) {
	const path = "/tmp/overlap_periodic_multiline.go"
	const content = `package p

func f() {
	use(
		1,
		1,
		1,
	)
}
`
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	res, err := h.PatchFile(context.Background(), domain.PatchFileRequest{
		FilePath: path,
		Patches: []domain.FilePatch{{
			Match:     "1,\n1,",
			Replace:   "2,\n2,",
			MatchMode: "normalized",
		}},
	})
	require.NoError(t, err)
	require.Len(t, res.Hits, 1)
	assert.Equal(t, 1, res.Hits[0], "periodic content must yield non-overlapping matches")

	got := string(fs.files[path])
	assert.Equal(t, 2, strings.Count(got, "2,"), "the single hit spans two elements")
	assert.Equal(t, 1, strings.Count(got, "1,"), "exactly one original element must remain")
}

// TestPatchFile_NormalizedTokenFallbackPeriodicMatch_NoOverlapPanic is the
// stage-4 sibling of the test above: line comments defeat the substring and
// multi-line stages, so matching falls through to the token stage, whose scan
// also advanced one token at a time and emitted overlapping hits on periodic
// token sequences — same applyRangeReplacements panic.
func TestPatchFile_NormalizedTokenFallbackPeriodicMatch_NoOverlapPanic(t *testing.T) {
	const path = "/tmp/overlap_periodic_tokens.go"
	const content = `package p

func f() {
	use(
		1, // one
		1, // two
		1,
	)
}
`
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	res, err := h.PatchFile(context.Background(), domain.PatchFileRequest{
		FilePath: path,
		Patches: []domain.FilePatch{{
			Match:     "1,\n1,",
			Replace:   "2,\n2,",
			MatchMode: "normalized",
		}},
	})
	require.NoError(t, err)
	require.Len(t, res.Hits, 1)
	assert.Equal(t, 1, res.Hits[0], "token-stage matches must be non-overlapping")
	assert.Equal(t, 1, strings.Count(string(fs.files[path]), "1,"), "exactly one original element must remain")
}

// TestPatchFunction_ReplaceAllPeriodicMultilineMatch_NonOverlapping is the
// PatchFunction side of backlog item 4: occurrence:-1 on a periodic multi-line
// match resolves overlapping hits, and the back-to-front splice replaces ALL
// three elements (silent corruption) instead of the single non-overlapping
// match. Non-overlapping semantics require exactly one hit here.
func TestPatchFunction_ReplaceAllPeriodicMultilineMatch_NonOverlapping(t *testing.T) {
	const path = "/tmp/overlap_periodic_func.go"
	const content = `package p

func f() {
	use(
		1,
		1,
		1,
	)
}
`
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "f",
		Patches: []domain.FunctionPatch{{
			Op:         domain.PatchOpReplace,
			Match:      "1,\n1,",
			Replace:    "2,\n2,",
			Occurrence: -1,
		}},
	})
	require.NoError(t, err)

	got := string(fs.files[path])
	assert.Equal(t, 2, strings.Count(got, "2,"), "one non-overlapping hit spans two elements")
	assert.Equal(t, 1, strings.Count(got, "1,"), "exactly one original element must remain")
}

// TestPatchFunction_OverlappingReplaceDelete_RejectedNotSpliced reproduces
// backlog item 12: two patches in ONE request whose resolved byte ranges
// overlap (replace lines 4-5 + delete lines 5-6) are spliced back-to-front
// with no disjointness check. The corrupted result even parses: `c := 3`
// survives although the delete covered it. Overlap must be a clear error and
// the file must stay untouched.
func TestPatchFunction_OverlappingReplaceDelete_RejectedNotSpliced(t *testing.T) {
	const path = "/tmp/overlap_edits_func.go"
	const content = `package p

func target() {
	a := 1
	b := 2
	c := 3
}
`
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "target",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, FromLine: 4, ToLine: 5, Replace: "x()"},
			{Op: domain.PatchOpDelete, FromLine: 5, ToLine: 6},
		},
	})
	require.Error(t, err, "overlapping replace+delete in one request must be rejected")
	assert.Contains(t, err.Error(), "overlap")
	assert.Equal(t, content, string(fs.files[path]), "file must be untouched after a rejected overlap")
}

// TestPatchFunction_AdjacentAndBoundaryEdits_StillApply guards the item-12
// fix against over-rejection: strictly adjacent ranges (replace line 4,
// delete line 5) and a zero-width insert touching another edit's start
// boundary are all legitimate and must keep working.
func TestPatchFunction_AdjacentAndBoundaryEdits_StillApply(t *testing.T) {
	const path = "/tmp/adjacent_edits_func.go"
	const content = `package p

func target() {
	a := 1
	b := 2
	c := 3
}
`
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "target",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, AtLine: 4, Replace: "x()"},
			{Op: domain.PatchOpDelete, AtLine: 5},
			{Op: domain.PatchOpInsertBefore, AtLine: 4, Code: "y()"},
		},
	})
	require.NoError(t, err, "adjacent and boundary-touching edits are disjoint and must apply")

	got := string(fs.files[path])
	assert.Contains(t, got, "y()")
	assert.Contains(t, got, "x()")
	assert.Contains(t, got, "c := 3")
	assert.NotContains(t, got, "a := 1")
	assert.NotContains(t, got, "b := 2")
	yIdx := strings.Index(got, "y()")
	xIdx := strings.Index(got, "x()")
	assert.Less(t, yIdx, xIdx, "insert_before at_line 4 must land before the replaced line 4")
}

// TestPatchDecl_OverlappingReplaceDelete_RejectedNotSpliced is the PatchDecl
// side of item 12: the same back-to-front splice with no disjointness check
// corrupts a composite literal value ("3," survives its own delete).
func TestPatchDecl_OverlappingReplaceDelete_RejectedNotSpliced(t *testing.T) {
	const path = "/tmp/overlap_edits_decl.go"
	const content = `package p

var v = []int{
	1,
	2,
	3,
}
`
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchDecl(context.Background(), domain.PatchDeclRequest{
		FilePath:   path,
		Identifier: "v",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, FromLine: 4, ToLine: 5, Replace: "9,"},
			{Op: domain.PatchOpDelete, FromLine: 5, ToLine: 6},
		},
	})
	require.Error(t, err, "overlapping replace+delete in one request must be rejected")
	assert.Contains(t, err.Error(), "overlap")
	assert.Equal(t, content, string(fs.files[path]), "file must be untouched after a rejected overlap")
}
