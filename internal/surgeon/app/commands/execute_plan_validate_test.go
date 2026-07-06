package commands_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAddFunc_RejectsTruncatedContent asserts that an AST action whose
// spliced result is syntactically invalid Go is rejected before writing.
// Before the fix, handleASTAction wrote the offset-spliced bytes without
// re-parsing, so truncated content ("func Broken() {") corrupted the file
// while the plan reported success.
func TestAddFunc_RejectsTruncatedContent(t *testing.T) {
	const path = "/tmp/exec_plan_truncated_add.go"
	const original = "package p\n\nfunc Existing() {}\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(original)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.ExecutePlan(context.Background(), domain.Plan{
		Actions: []domain.Action{{
			Action:   domain.ActionTypeAddFunc,
			FilePath: path,
			Content:  "func Broken() {",
		}},
	})

	require.Error(t, err, "truncated content must be rejected, not written")
	assert.Equal(t, original, string(fs.files[path]), "file must be left untouched on rejection")
}

// TestUpdateFunc_RejectsTruncatedContent covers the in-place splice path:
// update_func replaces a node range with the new content. Invalid content
// must be rejected before the write rather than corrupting the file.
func TestUpdateFunc_RejectsTruncatedContent(t *testing.T) {
	const path = "/tmp/exec_plan_truncated_update.go"
	const original = "package p\n\nfunc Target() {\n\treturn\n}\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(original)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.ExecutePlan(context.Background(), domain.Plan{
		Actions: []domain.Action{{
			Action:     domain.ActionTypeUpdateFunc,
			FilePath:   path,
			Identifier: "Target",
			Content:    "func Target() {",
		}},
	})

	require.Error(t, err, "truncated replacement must be rejected, not written")
	assert.Equal(t, original, string(fs.files[path]), "file must be left untouched on rejection")
}
