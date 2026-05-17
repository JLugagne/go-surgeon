package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPatch_MCP_ReplaceShorterMatch exercises the full MCP path to ensure
// the replacement text is preserved when it's shorter than the match.
func TestPatch_MCP_ReplaceShorterMatch(t *testing.T) {
	var capturedReq domain.PatchFunctionRequest
	mc := &mockCommands{
		patchFunctionFn: func(_ context.Context, req domain.PatchFunctionRequest) (domain.PatchFunctionResult, error) {
			capturedReq = req
			return domain.PatchFunctionResult{
				Applied: 1,
				Diff:    "--- a/f.go\n+++ b/f.go\n@@ -2,4 +2,4 @@ func F() {\n-\tx := longFunctionCall()\n+\tx := short()\n }\n",
			}, nil
		},
	}
	cs := setupTest(t, mc, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target":     "function",
		"file":       "f.go",
		"identifier": "F",
		"patches": []map[string]any{
			{"op": "replace", "match": "longFunctionCall()", "replace": "short()"},
		},
	})
	require.False(t, result.IsError, resultText(t, result))

	require.Len(t, capturedReq.Patches, 1)
	assert.Equal(t, "short()", capturedReq.Patches[0].Replace, "replacement must be preserved through MCP layer")
	assert.Equal(t, "longFunctionCall()", capturedReq.Patches[0].Match)

	text := resultText(t, result)
	assert.Contains(t, text, "HINT:", "should include hint about shorter replacement")
	assert.Contains(t, text, "shorter than input", "hint should mention shorter than input")

	require.NotNil(t, result.StructuredContent)
	buf, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var payload struct {
		Items []struct {
			Hint string `json:"hint"`
			Diff string `json:"diff"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(buf, &payload))
	require.Len(t, payload.Items, 1)
	assert.Contains(t, payload.Items[0].Hint, "shorter than input")
	assert.Contains(t, payload.Items[0].Diff, "short()", "diff should show the replacement")
}
