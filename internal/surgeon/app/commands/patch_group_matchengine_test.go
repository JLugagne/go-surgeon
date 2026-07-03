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

// TestPatchFunction_ReplaceAllShrinking_NoFalseDroppedContent reproduces
// backlog item 11: the dropped-content statement-count guard
// (validateNoDroppedStmts) assumes a SINGLE application. With occurrence:-1 the
// same multi-line shrinking replace lands on every hit, but the guard compares
// the fully-replaced body against single-application math (pre - matched + repl)
// and false-positives with PATCH_DROPPED_CONTENT. Two identical 3-statement
// blocks each replaced by a legitimate 2-statement block must be allowed.
func TestPatchFunction_ReplaceAllShrinking_NoFalseDroppedContent(t *testing.T) {
	const path = "/tmp/dropped_occurrence_all.go"
	const content = `package p

func f() {
	x := 1
	y := 2
	z := 3
	x := 1
	y := 2
	z := 3
}
`
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "f",
		Patches: []domain.FunctionPatch{{
			Op:         domain.PatchOpReplace,
			Match:      "x := 1\ny := 2\nz := 3",
			Replace:    "p := 1\nq := 2",
			Occurrence: -1,
		}},
	})
	require.NoError(t, err, "occurrence:-1 shrinking replace must not trip the dropped-content guard")

	got := string(fs.files[path])
	assert.Equal(t, 2, strings.Count(got, "p := 1"), "both matches must be replaced")
	assert.NotContains(t, got, "x := 1", "no original block should remain")
}

// TestPatchFunction_MultipleShrinkingReplaces_NoFalseDroppedContent is the
// sibling of the test above for the "multiple shrinking replaces in one
// request" half of item 11: two separate multi-line shrinking replace patches
// each drop a statement; the aggregate body math is correct but the
// per-application math false-positives.
func TestPatchFunction_MultipleShrinkingReplaces_NoFalseDroppedContent(t *testing.T) {
	const path = "/tmp/dropped_multi_replace.go"
	const content = `package p

func f() {
	a1 := 1
	a2 := 2
	a3 := 3
	b1 := 1
	b2 := 2
	b3 := 3
}
`
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "f",
		Patches: []domain.FunctionPatch{
			{Op: domain.PatchOpReplace, Match: "a1 := 1\na2 := 2\na3 := 3", Replace: "a1 := 1\na2 := 2"},
			{Op: domain.PatchOpReplace, Match: "b1 := 1\nb2 := 2\nb3 := 3", Replace: "b1 := 1\nb2 := 2"},
		},
	})
	require.NoError(t, err, "two shrinking replaces in one request must not trip the dropped-content guard")

	got := string(fs.files[path])
	assert.NotContains(t, got, "a3 := 3")
	assert.NotContains(t, got, "b3 := 3")
}

// TestPatchFile_ScopeDowngradeNotSticky reproduces backlog item 15: when an
// intermediate working source is unparseable, PatchFile downgrades scope to
// "all" for that patch — but it mutated the shared scope variable, so EVERY
// later patch also ran as "all". Here patch #2 leaves the source parseable
// again; patch #3 (code_only) must still filter a string-literal match.
func TestPatchFile_ScopeDowngradeNotSticky(t *testing.T) {
	const path = "/tmp/scope_sticky.go"
	const content = `package p

func run() {
	x := "keyword"
	_ = x
}
`
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchFile(context.Background(), domain.PatchFileRequest{
		FilePath: path,
		Scope:    "code_only",
		Patches: []domain.FilePatch{
			// #1: break syntax (unclosed inner block) — still parses at #1's start.
			{Match: "func run() {", Replace: "func run() { {"},
			// #2: repair syntax. Its start re-parses the broken source, which
			// fails and downgrades scope to "all" — must not leak to #3.
			{Match: "func run() { {", Replace: "func run() {"},
			// #3: code_only must skip the match inside the string literal.
			{Match: "keyword", Replace: "KEYWORD"},
		},
	})
	require.NoError(t, err)

	got := string(fs.files[path])
	assert.Contains(t, got, `"keyword"`, "code_only must still filter the string-literal match on patch #3")
	assert.NotContains(t, got, "KEYWORD", "scope downgrade on patch #2 must not stick to patch #3")
}

// TestPatchFile_OccurrenceBeyondMatchCount_Errors reproduces backlog item 26:
// an occurrence greater than the number of matches was a silent no-op that
// emitted a misleading "zero matches" warning even though matches existed.
// It must instead be a hard error, like patch_function.
func TestPatchFile_OccurrenceBeyondMatchCount_Errors(t *testing.T) {
	const path = "/tmp/occurrence_beyond.go"
	const content = `package p

func f() {
	a := foo
	b := foo
}
`
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchFile(context.Background(), domain.PatchFileRequest{
		FilePath: path,
		Patches: []domain.FilePatch{{
			Match:      "foo",
			Replace:    "bar",
			Occurrence: 5,
		}},
	})
	require.Error(t, err, "occurrence beyond the match count must be a hard error")
	assert.Contains(t, err.Error(), "occurrence 5")
	assert.Contains(t, err.Error(), "2 match", "the error must report the actual match count, not zero")
	assert.Equal(t, content, string(fs.files[path]), "file must be untouched")
}

// TestPatchFunction_RegexCaptureExpansion reproduces backlog item 27: $1
// capture groups are expanded by patch_file but inserted literally by
// patch_function match_regex replace. The replacement must expand $1 to the
// captured text, like patch_file does.
func TestPatchFunction_RegexCaptureExpansion(t *testing.T) {
	const path = "/tmp/regex_capture_func.go"
	const content = `package p

func f() {
	result := compute(alpha)
}
`
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "f",
		Patches: []domain.FunctionPatch{{
			Op:         domain.PatchOpReplace,
			MatchRegex: `compute\((\w+)\)`,
			Replace:    "fetch($1)",
		}},
	})
	require.NoError(t, err)

	got := string(fs.files[path])
	assert.Contains(t, got, "fetch(alpha)", "$1 must expand to the captured group")
	assert.NotContains(t, got, "fetch($1)", "$1 must not be inserted literally")
}

// TestPatchDecl_RegexCaptureExpansion is the patch_decl / occurrence:-1 side of
// item 27: every match_regex replace hit must expand its own capture groups.
func TestPatchDecl_RegexCaptureExpansion(t *testing.T) {
	const path = "/tmp/regex_capture_decl.go"
	const content = `package p

var pipeline = compute(a) + compute(b)
`
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchDecl(context.Background(), domain.PatchDeclRequest{
		FilePath:   path,
		Identifier: "pipeline",
		Patches: []domain.FunctionPatch{{
			Op:         domain.PatchOpReplace,
			MatchRegex: `compute\((\w+)\)`,
			Replace:    "fetch($1)",
			Occurrence: -1,
		}},
	})
	require.NoError(t, err)

	got := string(fs.files[path])
	assert.Contains(t, got, "fetch(a) + fetch(b)", "each hit must expand its own capture group")
	assert.NotContains(t, got, "fetch($1)", "$1 must not be inserted literally")
}

// TestPatchFunction_LineRangeInsertAfterUsesToLine reproduces backlog item 28:
// line-range insert_after anchored on from_line in patch_function (inserting
// after the FIRST line of the range) instead of after to_line. It must insert
// after to_line, matching patch_decl.
func TestPatchFunction_LineRangeInsertAfterUsesToLine(t *testing.T) {
	const path = "/tmp/insert_after_toline_func.go"
	const content = `package p

func f() {
	a := 1
	b := 2
	c := 3
}
`
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "f",
		Patches: []domain.FunctionPatch{{
			Op:       domain.PatchOpInsertAfter,
			FromLine: 4,
			ToLine:   5,
			Code:     "inserted()",
		}},
	})
	require.NoError(t, err)

	got := string(fs.files[path])
	bIdx := strings.Index(got, "b := 2")
	insIdx := strings.Index(got, "inserted()")
	cIdx := strings.Index(got, "c := 3")
	require.NotEqual(t, -1, insIdx)
	assert.Greater(t, insIdx, bIdx, "insert_after must land after to_line (b := 2), not after from_line")
	assert.Less(t, insIdx, cIdx, "insert_after must land before the following statement")
}

// TestPatchDecl_LineRangeInsertAfterUsesToLine guards that patch_decl already
// inserts after to_line (the semantics item 28 standardizes on).
func TestPatchDecl_LineRangeInsertAfterUsesToLine(t *testing.T) {
	const path = "/tmp/insert_after_toline_decl.go"
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
		Patches: []domain.FunctionPatch{{
			Op:       domain.PatchOpInsertAfter,
			FromLine: 4,
			ToLine:   5,
			Code:     "99,",
		}},
	})
	require.NoError(t, err)

	got := string(fs.files[path])
	twoIdx := strings.Index(got, "2,")
	insIdx := strings.Index(got, "99,")
	threeIdx := strings.Index(got, "3,")
	require.NotEqual(t, -1, insIdx)
	assert.Greater(t, insIdx, twoIdx, "insert_after must land after to_line (2,)")
	assert.Less(t, insIdx, threeIdx, "insert_after must land before the following element")
}
