package commands_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDirective_PatchFile_InsertCommentBetweenEmbedAndVar_Accepted: per the
// go:embed spec, "only blank lines and other comment lines are allowed
// between the directive and the declaration" — a comment keeps the directive
// attached (same comment group), so the patch must be accepted. The previous
// stricter rule ("directive must be the last comment in its group") also
// rejected idiomatic stacked //go:embed directives, blocking every patch to
// such files.
func TestDirective_PatchFile_InsertCommentBetweenEmbedAndVar_Accepted(t *testing.T) {
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
	require.NoError(t, err)
	content, err := fs.ReadFile(ctx, "f.go")
	require.NoError(t, err)
	assert.Contains(t, string(content), "//go:embed *.sql\n//nolint:unused\nvar migrationsFS embed.FS")
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
