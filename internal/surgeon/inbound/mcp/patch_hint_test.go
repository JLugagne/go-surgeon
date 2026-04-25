package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
		Hint string `json:"hint"`
	}
	require.NoError(t, json.Unmarshal(buf, &payload))
	assert.Contains(t, payload.Hint, "update object=func")
	assert.Contains(t, payload.Hint, "shorter than input")
}

// TestPatch_Function_ReplaceSameOrLonger_NoHint asserts that the hint is
// NOT attached when the replacement is the same length or longer than the
// match — the heuristic must not fire on healthy edits.
func TestPatch_Function_ReplaceSameOrLonger_NoHint(t *testing.T) {
	cs := setupTest(t, newShortReplaceCommands(), &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target":     "function",
		"file":       "foo.go",
		"identifier": "Foo",
		"patches": []map[string]any{
			{"op": "replace", "match": "abc", "replace": "abcdef"},
		},
	})
	require.False(t, result.IsError, resultText(t, result))
	assert.NotContains(t, resultText(t, result), "HINT:")

	require.NotNil(t, result.StructuredContent)
	buf, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var payload struct {
		Hint string `json:"hint"`
	}
	require.NoError(t, json.Unmarshal(buf, &payload))
	assert.Empty(t, payload.Hint)
}

// TestPatch_Decl_ReplaceShorter_AddsHint mirrors the function-target test
// for target=decl — patch_decl must surface the same hint when the splice
// would shrink the decl value.
func TestPatch_Decl_ReplaceShorter_AddsHint(t *testing.T) {
	cs := setupTest(t, newShortReplaceCommands(), &mockQueries{})

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
		"target":     "function",
		"file":       "foo.go",
		"identifier": "Foo",
		"patches": []map[string]any{
			{"op": "insert_before", "match": "anchor", "code": "log.Println()"},
		},
	})
	require.False(t, result.IsError, resultText(t, result))
	assert.NotContains(t, resultText(t, result), "HINT:")
}

// TestDescribeTool_Patch_ListsLimitations asserts that 'describe_tool name=patch'
// surfaces a Limitations section in text mode covering the three known
// edge cases (multi-line replacement, tabs/escapes, struct-literal field
// insertion) along with the recommended workaround.
func TestDescribeTool_Patch_ListsLimitations(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "describe_tool",
		Arguments: map[string]any{"name": "patch"},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	text := resultText(t, result)
	assert.Contains(t, text, "limitations:")
	assert.Contains(t, text, "multi-line replacement")
	assert.Contains(t, text, "tabs/escapes")
	assert.Contains(t, text, "struct-literal")
	assert.Contains(t, text, "update object=func")

	// JSON form must also expose the limitations array so non-text clients
	// can branch on it programmatically.
	jsonResult, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "describe_tool",
		Arguments: map[string]any{"name": "patch", "format": "json"},
	})
	require.NoError(t, err)
	require.False(t, jsonResult.IsError)
	require.NotNil(t, jsonResult.StructuredContent)
	buf, err := json.Marshal(jsonResult.StructuredContent)
	require.NoError(t, err)
	var payload struct {
		Tool struct {
			Limitations []string `json:"limitations"`
		} `json:"tool"`
	}
	require.NoError(t, json.Unmarshal(buf, &payload))
	require.GreaterOrEqual(t, len(payload.Tool.Limitations), 3)
	joined := strings.Join(payload.Tool.Limitations, "\n")
	assert.Contains(t, joined, "multi-line replacement")
	assert.Contains(t, joined, "update object=func")
}

// TestPatchToolDescription_MentionsUpdateFallback asserts the tool's
// description string itself surfaces the "use update for multi-line edits"
// guidance — agents that read the tool catalog from listTools() should see
// it without having to call describe_tool.
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
