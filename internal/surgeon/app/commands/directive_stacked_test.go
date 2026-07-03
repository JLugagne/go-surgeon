package commands_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/require"
)

// TestPatchFunction_StackedDirectivesAllowed asserts that a file containing
// stacked //go:embed directives (idiomatic Go: several embeds in one doc
// group) can still be patched. Before the fix, checkDirectivesIntact
// required every directive to be the LAST comment of its group, so ANY
// patch to such a file failed with PATCH_BREAKS_DIRECTIVE.
func TestPatchFunction_StackedDirectivesAllowed(t *testing.T) {
	const path = "/tmp/stacked_embed.go"
	src := "package assets\n\nimport \"embed\"\n\n//go:embed image/*\n//go:embed html/index.html\nvar content embed.FS\n\nfunc Answer() int {\n\treturn 1\n}\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(src)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "Answer",
		Patches: []domain.FunctionPatch{{
			Op:      domain.PatchOpReplace,
			Match:   "return 1",
			Replace: "return 42",
		}},
	})
	require.NoError(t, err, "stacked //go:embed directives must not block unrelated patches")
}
