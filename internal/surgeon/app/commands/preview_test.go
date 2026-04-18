package commands_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecutePlan_PreviewDoesNotMutateDisk asserts that running a plan with
// Preview=true surfaces a unified diff but leaves the backing filesystem
// untouched. Covers the create_file + update_func path so two distinct
// write branches in executeAction are exercised.
func TestExecutePlan_PreviewDoesNotMutateDisk(t *testing.T) {
	const existing = `package pkg

func Greet() string {
	return "hi"
}
`
	const newPath = "/tmp/preview_new.go"
	const existingPath = "/tmp/preview_existing.go"

	fs := &mockFS{files: map[string][]byte{
		existingPath: []byte(existing),
	}}
	// Snapshot the original bytes so we can verify nothing was touched.
	originalExisting := string(fs.files[existingPath])
	_, hasNewBefore := fs.files[newPath]
	require.False(t, hasNewBefore, "precondition: target create file must not exist yet")

	h := commands.NewExecutePlanHandler(fs)

	plan := domain.Plan{
		Preview: true,
		Actions: []domain.Action{
			{
				Action:   domain.ActionTypeCreateFile,
				FilePath: newPath,
				Content:  "package preview\n\nfunc Hello() string { return \"hello\" }\n",
			},
			{
				Action:     domain.ActionTypeUpdateFunc,
				FilePath:   existingPath,
				Identifier: "Greet",
				Content:    "func Greet() string {\n\treturn \"hello\"\n}",
			},
		},
	}

	result, err := h.ExecutePlan(context.Background(), plan)
	require.NoError(t, err)

	// Result flags preview + carries a diff.
	assert.True(t, result.Preview, "PlanResult.Preview must reflect plan.Preview")
	assert.NotEmpty(t, result.Diff, "Diff must be populated when Preview=true")
	assert.Contains(t, result.Diff, "Hello", "diff should mention the new file's content")
	assert.Contains(t, result.Diff, "hello", "diff should mention the updated return value")

	// Files that would be touched are reported.
	assert.ElementsMatch(t, []string{newPath, existingPath}, result.Files)
	assert.Equal(t, 2, result.FilesModified)

	// Disk-equivalent backing filesystem is untouched:
	//   - the new file did NOT appear
	//   - the existing file still has its original content
	_, hasNewAfter := fs.files[newPath]
	assert.False(t, hasNewAfter, "preview must not create the new file")
	assert.Equal(t, originalExisting, string(fs.files[existingPath]),
		"preview must not mutate the existing file")
}

// TestTagStruct_PreviewDoesNotMutateDisk asserts that setting Preview=true
// on a tag operation (a non-plan handler) yields no filesystem side-effects.
// This covers the per-handler preview branch (tag_struct.go).
func TestTagStruct_PreviewDoesNotMutateDisk(t *testing.T) {
	const initial = `package pkg

type User struct {
	Name  string
	Email string
}
`
	const path = "/tmp/preview_tag.go"

	fs := &mockFS{files: map[string][]byte{path: []byte(initial)}}
	h := commands.NewExecutePlanHandler(fs)

	err := h.TagStruct(context.Background(), domain.TagRequest{
		FilePath:   path,
		StructName: "User",
		AutoFormat: "json",
		Preview:    true,
	})
	require.NoError(t, err)

	// Content on disk (well, in the mockFS) is unchanged.
	assert.Equal(t, initial, string(fs.files[path]),
		"TagStruct with Preview=true must not write to the file")
}
