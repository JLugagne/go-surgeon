package commands_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecutePlan_PreviewDeleteFile_ShowsDeletionDiff asserts that a
// preview of a delete_file action reports the deletion: the diff must show
// the removed content and the file must be listed as modified. An empty
// "0 files modified" preview makes agents believe the delete is a no-op.
func TestExecutePlan_PreviewDeleteFile_ShowsDeletionDiff(t *testing.T) {
	const path = "/tmp/preview_delete_target.go"
	const content = "package pkg\n\nfunc Gone() {}\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	result, err := h.ExecutePlan(context.Background(), domain.Plan{
		Preview: true,
		Actions: []domain.Action{{Action: domain.ActionTypeDeleteFile, FilePath: path}},
	})
	require.NoError(t, err)

	assert.True(t, result.Preview)
	assert.Contains(t, result.Diff, path, "deletion must appear in the preview diff")
	assert.Contains(t, result.Diff, "-func Gone() {}", "removed lines must appear in the preview diff")
	assert.Contains(t, result.Files, path, "deleted file must be listed in Files")
	assert.Equal(t, 1, result.FilesModified)

	// The real filesystem must be untouched by the preview.
	_, ok := fs.files[path]
	assert.True(t, ok, "preview must not delete the real file")
}

// TestExecutePlan_PreviewDeleteThenCreate_SamePath asserts preview parity
// with a real run: deleting a file and re-creating it in the same plan
// succeeds on disk (delete removes the file, create sees it gone), so the
// preview must not fail with ErrFileAlreadyExists.
func TestExecutePlan_PreviewDeleteThenCreate_SamePath(t *testing.T) {
	const path = "/tmp/preview_recreate.go"
	fs := &mockFS{files: map[string][]byte{path: []byte("package pkg\n\nfunc Old() {}\n")}}
	h := commands.NewExecutePlanHandler(fs)

	result, err := h.ExecutePlan(context.Background(), domain.Plan{
		Preview: true,
		Actions: []domain.Action{
			{Action: domain.ActionTypeDeleteFile, FilePath: path},
			{Action: domain.ActionTypeCreateFile, FilePath: path, Content: "func New() {}\n"},
		},
	})
	require.NoError(t, err, "preview must match the non-preview outcome (delete then create succeeds)")
	assert.Contains(t, result.Diff, "func New()")
}
