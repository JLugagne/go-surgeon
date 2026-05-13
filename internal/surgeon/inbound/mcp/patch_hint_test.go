package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shortReplaceFn returns a fake patch_function/patch_decl handler whose
// command-layer result reports success. The hint logic lives in the MCP
// handler (it inspects the input ops, not the command result), so the
// mocked applied count is enough.
func newShortReplaceCommands() *mockCommands {
	return &mockCommands{}
}

// TestPatch_Function_ReplaceShorter_AddsHint asserts that op=replace with
// a replacement shorter than the match attaches the "try update object=func"
// hint to both the text and StructuredContent of patch (target=function).
func TestPatch_Function_ReplaceShorter_AddsHint(t *testing.T) {
	cs := setupTest(t, newShortReplaceCommands(), &mockQueries{})

	// Single-target shape — exercises the top-level fields.
	result := callTool(t, cs, "patch", map[string]any{
		"target":     "function",
		"file":       "foo.go",
		"identifier": "Foo",
		"patches": []map[string]any{
			{"op": "replace", "match": "verylongmatchcontent", "replace": "x"},
		},
	})
	require.False(t, result.IsError, resultText(t, result))

	text := resultText(t, result)
	assert.Contains(t, text, "HINT:")
	assert.Contains(t, text, "update object=func")

	require.NotNil(t, result.StructuredContent)
	buf, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var payload struct {
		Items []struct {
			Hint string `json:"hint"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(buf, &payload))
	require.Len(t, payload.Items, 1)
	assert.Contains(t, payload.Items[0].Hint, "update object=func")
	assert.Contains(t, payload.Items[0].Hint, "shorter than input")
}

// TestPatch_Function_ReplaceSameOrLonger_NoHint asserts that the hint is
// NOT attached when the replacement is the same length or longer than the
// match — the heuristic must not fire on healthy edits.
func TestPatch_Function_ReplaceSameOrLonger_NoHint(t *testing.T) {
	cs := setupTest(t, newShortReplaceCommands(), &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target": "function",
		"items": []map[string]any{
			{
				"file":       "foo.go",
				"identifier": "Foo",
				"patches": []map[string]any{
					{"op": "replace", "match": "abc", "replace": "abcdef"},
				},
			},
		},
	})
	require.False(t, result.IsError, resultText(t, result))
	assert.NotContains(t, resultText(t, result), "HINT:")

	require.NotNil(t, result.StructuredContent)
	buf, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var payload struct {
		Items []struct {
			Hint string `json:"hint"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(buf, &payload))
	require.Len(t, payload.Items, 1)
	assert.Empty(t, payload.Items[0].Hint)
}

// TestPatch_Decl_ReplaceShorter_AddsHint mirrors the function-target test
// for target=decl — patch_decl must surface the same hint when the splice
// would shrink the decl value.
func TestPatch_Decl_ReplaceShorter_AddsHint(t *testing.T) {
	cs := setupTest(t, newShortReplaceCommands(), &mockQueries{})

	// Single-target shape: top-level file + identifier + patches.
	result := callTool(t, cs, "patch", map[string]any{
		"target":     "decl",
		"file":       "foo.go",
		"identifier": "banner",
		"patches": []map[string]any{
			{"op": "replace", "match": "longoriginalvalue", "replace": "tiny"},
		},
	})
	require.False(t, result.IsError, resultText(t, result))
	assert.Contains(t, resultText(t, result), "HINT:")
	assert.Contains(t, resultText(t, result), "update object=func")
}

// TestPatch_File_ReplaceShorter_AddsHint covers target=file (whole-file
// substitution). Same heuristic, same hint text.
func TestPatch_File_ReplaceShorter_AddsHint(t *testing.T) {
	cs := setupTest(t, newShortReplaceCommands(), &mockQueries{})

	// Single-target shape on target=file: top-level file + patches (no identifier).
	result := callTool(t, cs, "patch", map[string]any{
		"target": "file",
		"file":   "foo.go",
		"patches": []map[string]any{
			{"match": "longoldname", "replace": "x"},
		},
	})
	require.False(t, result.IsError, resultText(t, result))
	text := resultText(t, result)
	assert.Contains(t, text, "HINT:")
	assert.Contains(t, text, "update object=func")
}

// TestPatch_NonReplaceOp_NoHint asserts that ops other than replace
// (e.g. insert_before) never trigger the hint, even if their fields look
// shorter — the hint is gated on op="replace".
func TestPatch_NonReplaceOp_NoHint(t *testing.T) {
	cs := setupTest(t, newShortReplaceCommands(), &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target": "function",
		"items": []map[string]any{
			{
				"file":       "foo.go",
				"identifier": "Foo",
				"patches": []map[string]any{
					{"op": "insert_before", "match": "anchor", "code": "log.Println()"},
				},
			},
		},
	})
	require.False(t, result.IsError, resultText(t, result))
	assert.NotContains(t, resultText(t, result), "HINT:")
}

// TestPatchToolDescription_MentionsUpdateFallback asserts the tool's
// description string itself surfaces the "use update for multi-line edits"
// guidance — agents that read the tool catalog from listTools() should see
// it without having to shell out to `go-surgeon discovery patch`.
func TestPatchToolDescription_MentionsUpdateFallback(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	listed, err := cs.ListTools(context.Background(), nil)
	require.NoError(t, err)
	var patchDescription string
	for _, tool := range listed.Tools {
		if tool.Name == "patch" {
			patchDescription = tool.Description
			break
		}
	}
	require.NotEmpty(t, patchDescription, "patch tool not registered")
	assert.Contains(t, patchDescription, "update")
	assert.Contains(t, patchDescription, "multi-line")
}
