package commands_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPatchStructBulk_HappyPath_ThreeItemsTwoFiles(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()

	setFile(fs, "a.go", `package p

type Alpha struct {
	ID string
}

type Bravo struct {
	ID string
}
`)
	setFile(fs, "b.go", `package p

type Charlie struct {
	ID string
}
`)

	res, err := h.PatchStructBulk(ctx, domain.PatchStructBulkRequest{
		Items: []domain.PatchStructBulkItem{
			{FilePath: "a.go", Identifier: "Alpha", Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpAddField, Name: "Preview", Type: "bool"},
			}},
			{FilePath: "a.go", Identifier: "Bravo", Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpAddField, Name: "Preview", Type: "bool"},
			}},
			{FilePath: "b.go", Identifier: "Charlie", Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpAddField, Name: "Preview", Type: "bool"},
			}},
		},
	})

	require.NoError(t, err)
	require.Len(t, res.Items, 3)
	assert.Equal(t, 3, res.Applied)
	assert.False(t, res.Preview)
	assert.NotEmpty(t, res.Diff)

	for _, path := range []string{"a.go", "b.go"} {
		assert.Contains(t, getFile(fs, path), "Preview bool", "file %s should now have the new field", path)
	}
	assert.Equal(t, 2, strings.Count(getFile(fs, "a.go"), "Preview bool"))
}

func TestPatchStructBulk_RollbackOnSecondItemFailure(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()

	original := `package p

type Alpha struct {
	ID string
}
`
	setFile(fs, "a.go", original)
	setFile(fs, "b.go", `package p

type Bravo struct {
	ID string
}
`)

	_, err := h.PatchStructBulk(ctx, domain.PatchStructBulkRequest{
		Items: []domain.PatchStructBulkItem{
			{FilePath: "a.go", Identifier: "Alpha", Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpAddField, Name: "Preview", Type: "bool"},
			}},
			{FilePath: "b.go", Identifier: "DoesNotExist", Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpAddField, Name: "Preview", Type: "bool"},
			}},
		},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "item #2")
	assert.Contains(t, err.Error(), "DoesNotExist")
	assert.Equal(t, original, getFile(fs, "a.go"), "item #1's file must be pristine after rollback")
}

func TestPatchStructBulk_SoftCapRejectsTooManyItems(t *testing.T) {
	ctx := context.Background()
	h, _ := newPatchHandler()

	items := make([]domain.PatchStructBulkItem, 21)
	for i := range items {
		items[i] = domain.PatchStructBulkItem{
			FilePath:   "x.go",
			Identifier: "X",
			Patches:    []domain.StructPatch{{Op: domain.StructPatchOpAddField, Name: "P", Type: "bool"}},
		}
	}

	_, err := h.PatchStructBulk(ctx, domain.PatchStructBulkRequest{Items: items})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max 20 items")
	assert.Contains(t, err.Error(), "got 21")
}

func TestPatchStructBulk_PreviewDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()

	original := `package p

type Alpha struct {
	ID string
}
`
	setFile(fs, "a.go", original)

	res, err := h.PatchStructBulk(ctx, domain.PatchStructBulkRequest{
		Preview: true,
		Items: []domain.PatchStructBulkItem{
			{FilePath: "a.go", Identifier: "Alpha", Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpAddField, Name: "Preview", Type: "bool"},
			}},
		},
	})

	require.NoError(t, err)
	assert.True(t, res.Preview)
	assert.NotEmpty(t, res.Diff)
	assert.Equal(t, original, getFile(fs, "a.go"), "preview must not mutate the file")
}
