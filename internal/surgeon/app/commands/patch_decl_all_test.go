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

// TestPatchDecl_OccurrenceAllApplies asserts occurrence=-1 (documented as
// \"apply to all matches\" in the shared patch op schema) patches every hit
// in a decl value. Before the fix patch_decl silently patched only the
// first hit while reporting success.
func TestPatchDecl_OccurrenceAllApplies(t *testing.T) {
	const path = "/tmp/patch_decl_all.go"
	src := "package msg\n\nvar banner = `v1\nv1\nv1`\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(src)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchDecl(context.Background(), domain.PatchDeclRequest{
		FilePath:   path,
		Identifier: "banner",
		Patches: []domain.FunctionPatch{{
			Op:         domain.PatchOpReplace,
			Match:      "v1",
			Replace:    "v2",
			Occurrence: -1,
		}},
	})
	require.NoError(t, err)

	updated := string(fs.files[path])
	assert.Equal(t, 3, strings.Count(updated, "v2"), "all occurrences must be replaced: %s", updated)
	assert.NotContains(t, updated, "v1", "no occurrence may be left behind")
}
