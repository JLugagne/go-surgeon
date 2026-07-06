package mcp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPatchBulk_Header_ReflectsAppliedItems asserts that the bulk result
// header counts the items that actually applied (per-item Applied > 0), not a
// hardcoded N/N. Item 33: a batch where one item is a no-op must not report
// "2/2 items applied".
func TestPatchBulk_Header_ReflectsAppliedItems(t *testing.T) {
	commands := &mockCommands{
		patchInterfaceFn: func(_ context.Context, req domain.PatchInterfaceRequest) (domain.PatchInterfaceResult, error) {
			if req.Identifier == "Noop" {
				return domain.PatchInterfaceResult{Applied: 0}, nil
			}
			return domain.PatchInterfaceResult{Applied: 1, Diff: "d"}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target": "interface",
		"items": []map[string]any{
			{"file": "a.go", "identifier": "Applied", "patches": []map[string]any{{"op": "add_method", "signature": "Close() error"}}},
			{"file": "b.go", "identifier": "Noop", "patches": []map[string]any{{"op": "remove_method", "name": "Gone"}}},
		},
	})
	require.False(t, result.IsError, resultText(t, result))
	text := resultText(t, result)
	assert.Contains(t, text, "OK: 1/2 items applied", "header must reflect the one item that applied, got: %s", text)
	assert.NotContains(t, text, "2/2 items applied")
}

// TestPatchBulk_Preview_SameFile_Warns asserts that a multi-item preview that
// targets the same file surfaces a warning about non-composition (each item is
// previewed independently against the on-disk state). Item 33.
func TestPatchBulk_Preview_SameFile_Warns(t *testing.T) {
	commands := &mockCommands{
		patchFileFn: func(_ context.Context, _ domain.PatchFileRequest) (domain.PatchFileResult, error) {
			return domain.PatchFileResult{Applied: 1, Diff: "d", Hits: []int{1}}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target":  "file",
		"preview": true,
		"items": []map[string]any{
			{"file": "same.go", "patches": []map[string]any{{"match": "a", "replace": "b"}}},
			{"file": "same.go", "patches": []map[string]any{{"match": "c", "replace": "d"}}},
		},
	})
	require.False(t, result.IsError, resultText(t, result))
	text := strings.ToLower(resultText(t, result))
	assert.Contains(t, text, "same file", "same-file preview must warn about non-composition, got: %s", resultText(t, result))
}

// TestPatch_Preview_MentionsGoimportsFinalization asserts that preview output
// tells the caller that formatting + imports are only finalized on write, so
// the previewed diff can differ from the written result. Item 25.
func TestPatch_Preview_MentionsGoimportsFinalization(t *testing.T) {
	commands := &mockCommands{
		patchFunctionFn: func(_ context.Context, _ domain.PatchFunctionRequest) (domain.PatchFunctionResult, error) {
			return domain.PatchFunctionResult{Applied: 1, Preview: true, Diff: "some diff"}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target":     "function",
		"file":       "foo.go",
		"identifier": "Foo",
		"preview":    true,
		"patches":    []map[string]any{{"op": "replace", "match": "foo", "replace": "bar"}},
	})
	require.False(t, result.IsError, resultText(t, result))
	text := resultText(t, result)
	assert.Contains(t, text, "goimports", "preview must note goimports finalization, got: %s", text)
}

// TestPatch_NonPreview_NoGoimportsNote asserts the finalization note is
// preview-only and does not clutter real-write output. Item 25.
func TestPatch_NonPreview_NoGoimportsNote(t *testing.T) {
	commands := &mockCommands{
		patchFunctionFn: func(_ context.Context, _ domain.PatchFunctionRequest) (domain.PatchFunctionResult, error) {
			return domain.PatchFunctionResult{Applied: 1, Diff: "some diff"}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target":     "function",
		"file":       "foo.go",
		"identifier": "Foo",
		"patches":    []map[string]any{{"op": "replace", "match": "foo", "replace": "bar"}},
	})
	require.False(t, result.IsError, resultText(t, result))
	assert.NotContains(t, resultText(t, result), "goimports")
}
