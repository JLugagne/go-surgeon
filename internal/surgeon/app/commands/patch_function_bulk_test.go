package commands_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPatchFunctionBulk_HappyPath_ThreeItemsTwoFiles(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()

	setFile(fs, "a.go", `package p

func Alpha() {
	x := 1
	_ = x
}

func Bravo() {
	x := 1
	_ = x
}
`)
	setFile(fs, "b.go", `package p

func Charlie() {
	x := 1
	_ = x
}
`)

	res, err := h.PatchFunctionBulk(ctx, domain.PatchFunctionBulkRequest{
		Items: []domain.PatchFunctionBulkItem{
			{FilePath: "a.go", Identifier: "Alpha", Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "x := 1", Replace: "x := 2"},
			}},
			{FilePath: "a.go", Identifier: "Bravo", Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "x := 1", Replace: "x := 2"},
			}},
			{FilePath: "b.go", Identifier: "Charlie", Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "x := 1", Replace: "x := 2"},
			}},
		},
	})

	require.NoError(t, err)
	require.Len(t, res.Items, 3)
	assert.Equal(t, 3, res.Applied)
	assert.False(t, res.Preview)
	assert.NotEmpty(t, res.Diff)

	assert.Equal(t, 2, strings.Count(getFile(fs, "a.go"), "x := 2"))
	assert.Equal(t, 1, strings.Count(getFile(fs, "b.go"), "x := 2"))
	assert.NotContains(t, getFile(fs, "a.go"), "x := 1")
	assert.NotContains(t, getFile(fs, "b.go"), "x := 1")
}

func TestPatchFunctionBulk_RollbackOnSecondItemFailure(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()

	original := `package p

func Alpha() {
	x := 1
	_ = x
}
`
	setFile(fs, "a.go", original)
	setFile(fs, "b.go", `package p

func Bravo() {
	x := 1
	_ = x
}
`)

	_, err := h.PatchFunctionBulk(ctx, domain.PatchFunctionBulkRequest{
		Items: []domain.PatchFunctionBulkItem{
			{FilePath: "a.go", Identifier: "Alpha", Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "x := 1", Replace: "x := 2"},
			}},
			{FilePath: "b.go", Identifier: "DoesNotExist", Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "x := 1", Replace: "x := 2"},
			}},
		},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "item #2")
	assert.Contains(t, err.Error(), "DoesNotExist")
	assert.Equal(t, original, getFile(fs, "a.go"), "item #1's file must be pristine after rollback")
}

func TestPatchFunctionBulk_SoftCapRejectsTooManyItems(t *testing.T) {
	ctx := context.Background()
	h, _ := newPatchHandler()

	items := make([]domain.PatchFunctionBulkItem, 21)
	for i := range items {
		items[i] = domain.PatchFunctionBulkItem{
			FilePath:   "x.go",
			Identifier: "X",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "a", Replace: "b"},
			},
		}
	}

	_, err := h.PatchFunctionBulk(ctx, domain.PatchFunctionBulkRequest{Items: items})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max 20 items")
	assert.Contains(t, err.Error(), "got 21")
}

func TestPatchFunctionBulk_PreviewDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()

	original := `package p

func Alpha() {
	x := 1
	_ = x
}
`
	setFile(fs, "a.go", original)

	res, err := h.PatchFunctionBulk(ctx, domain.PatchFunctionBulkRequest{
		Preview: true,
		Items: []domain.PatchFunctionBulkItem{
			{FilePath: "a.go", Identifier: "Alpha", Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "x := 1", Replace: "x := 2"},
			}},
		},
	})

	require.NoError(t, err)
	assert.True(t, res.Preview)
	assert.NotEmpty(t, res.Diff)
	assert.Equal(t, original, getFile(fs, "a.go"), "preview must not mutate the file")
}
