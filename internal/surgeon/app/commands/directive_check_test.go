package commands_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDirective_PatchFile_InsertCommentBetweenEmbedAndVar_Rejected(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

import "embed"

//go:embed *.sql
var migrationsFS embed.FS
`)
	_, err := h.PatchFile(ctx, domain.PatchFileRequest{
		FilePath: "f.go",
		Patches: []domain.FilePatch{
			// Insert //nolint comment between //go:embed and var declaration.
			{Match: "//go:embed *.sql\nvar", Replace: "//go:embed *.sql\n//nolint:unused\nvar"},
		},
	})
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	assert.Equal(t, "PATCH_BREAKS_DIRECTIVE", domErr.Code)
	assert.Contains(t, domErr.Message, "//go:embed")
}

func TestDirective_PatchFile_InsertAfterTarget_Accepted(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

import "embed"

//go:embed *.sql
var migrationsFS embed.FS
`)
	// Inserting a new declaration after the target variable is safe.
	_, err := h.PatchFile(ctx, domain.PatchFileRequest{
		FilePath: "f.go",
		Patches: []domain.FilePatch{
			{Match: "var migrationsFS embed.FS\n", Replace: "var migrationsFS embed.FS\n\nvar otherVar = 42\n"},
		},
	})
	require.NoError(t, err)
}
