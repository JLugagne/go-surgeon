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

func callDescribe(t *testing.T, cs *mcp.ClientSession, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "describe_tool",
		Arguments: args,
	})
	require.NoError(t, err)
	return result
}

// TestDescribeTool_NoArgs_ListsAllCategories asserts the grouped
// overview covers every category header and every tool name.
func TestDescribeTool_NoArgs_ListsAllCategories(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	text := resultText(t, callDescribe(t, cs, map[string]any{}))

	// Category headers (prose labels) must all appear.
	for _, header := range []string{"EXPLORE", "REFS & RENAME", "EDIT", "INTERFACE", "CODE GENERATION", "VALIDATE", "BATCH", "META"} {
		assert.Contains(t, text, header, "missing category header: %s", header)
	}
	// A sampling of tool names from different categories.
	for _, name := range []string{"overview", "patch_function", "find_references", "build_check", "execute_plan", "describe_tool"} {
		assert.Contains(t, text, name, "missing tool name: %s", name)
	}
}

// TestDescribeTool_Name_SingleToolDetail asserts name=X returns the
// per-tool detail view: summary line + example + related-tools line.
func TestDescribeTool_Name_SingleToolDetail(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	text := resultText(t, callDescribe(t, cs, map[string]any{"name": "patch_function"}))

	assert.Contains(t, text, "patch_function (edit)")
	assert.Contains(t, text, "example:")
	assert.Contains(t, text, "see also:")
	// Unrelated tools should NOT appear in single-tool view.
	assert.NotContains(t, text, "overview ")
}

// TestDescribeTool_Category_FiltersToGroup asserts category=X only
// emits that category.
func TestDescribeTool_Category_FiltersToGroup(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	text := resultText(t, callDescribe(t, cs, map[string]any{"category": "edit"}))

	assert.Contains(t, text, "EDIT")
	assert.Contains(t, text, "patch_function")
	// Other categories should not appear.
	assert.NotContains(t, text, "EXPLORE")
	assert.NotContains(t, text, "VALIDATE")
}

// TestDescribeTool_UnknownName_Errors asserts the lookup surfaces a
// clear error for typos / renames.
func TestDescribeTool_UnknownName_Errors(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callDescribe(t, cs, map[string]any{"name": "no_such_tool"})
	require.True(t, result.IsError)
	text := resultText(t, result)
	assert.True(t, strings.Contains(text, "unknown tool"))
}

// TestDescribeTool_NameAndCategory_MutuallyExclusive rejects both at
// once — the two axes are exclusive by design.
func TestDescribeTool_NameAndCategory_MutuallyExclusive(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callDescribe(t, cs, map[string]any{"name": "symbol", "category": "explore"})
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "mutually exclusive")
}

// TestDescribeTool_CatalogCoversEveryRegisteredTool catches drift: if
// a new tool gets registered without a catalog entry (or vice versa),
// this test fails — the catalog is the agent-facing index and must
// stay complete.
func TestDescribeTool_CatalogCoversEveryRegisteredTool(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	listed, err := cs.ListTools(context.Background(), nil)
	require.NoError(t, err)

	catalog := resultText(t, callDescribe(t, cs, map[string]any{}))
	for _, tool := range listed.Tools {
		assert.Contains(t, catalog, tool.Name, "describe_tool catalog is missing registered tool %q — add an entry to toolCatalog", tool.Name)
	}
}

// TestDescribeTool_Format_JSON_ListAll asserts format=json returns
// StructuredContent containing every catalog entry and that each entry
// round-trips through encoding/json.Marshal cleanly.
func TestDescribeTool_Format_JSON_ListAll(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callDescribe(t, cs, map[string]any{"format": "json"})
	require.False(t, result.IsError)
	require.NotNil(t, result.StructuredContent, "format=json must populate StructuredContent")

	// Round-trip: marshal the structured payload back to JSON, then
	// unmarshal into a minimal shape to validate keys exist and every
	// registered tool is present.
	buf, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err, "StructuredContent must be json-marshalable")

	var payload struct {
		Tools []struct {
			Name     string `json:"name"`
			Category string `json:"category"`
			Summary  string `json:"summary"`
			Example  string `json:"example,omitempty"`
			Related  string `json:"related,omitempty"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(buf, &payload))

	// Cross-check against the server's registered tool list — the JSON
	// payload must cover every registered tool, same invariant as the
	// text-mode drift test.
	listed, err := cs.ListTools(context.Background(), nil)
	require.NoError(t, err)
	names := map[string]bool{}
	for _, tool := range payload.Tools {
		require.NotEmpty(t, tool.Name, "every json entry must have a name")
		require.NotEmpty(t, tool.Category, "every json entry must have a category")
		require.NotEmpty(t, tool.Summary, "every json entry must have a summary")
		names[tool.Name] = true
	}
	for _, tool := range listed.Tools {
		assert.True(t, names[tool.Name], "format=json payload missing registered tool %q", tool.Name)
	}

	// Text body (Content[0]) must also be present for stdio clients.
	require.NotEmpty(t, result.Content, "format=json must still emit a text body for stdio clients")
	body := resultText(t, result)
	var echo map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &echo), "text body must be a compact JSON string")
}

// TestDescribeTool_Format_JSON_SingleTool asserts name=X & format=json
// returns a {tool: {...}} payload with the catalog fields populated.
func TestDescribeTool_Format_JSON_SingleTool(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callDescribe(t, cs, map[string]any{"name": "patch_function", "format": "json"})
	require.False(t, result.IsError)
	require.NotNil(t, result.StructuredContent)

	buf, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var payload struct {
		Tool struct {
			Name     string `json:"name"`
			Category string `json:"category"`
			Summary  string `json:"summary"`
			Example  string `json:"example"`
			Related  string `json:"related"`
		} `json:"tool"`
	}
	require.NoError(t, json.Unmarshal(buf, &payload))
	assert.Equal(t, "patch_function", payload.Tool.Name)
	assert.Equal(t, "edit", payload.Tool.Category)
	assert.NotEmpty(t, payload.Tool.Summary)
	assert.NotEmpty(t, payload.Tool.Example)
}

// TestDescribeTool_Format_InvalidRejected rejects format values that
// are not "text" or "json" — lets agents catch typos fast.
func TestDescribeTool_Format_InvalidRejected(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callDescribe(t, cs, map[string]any{"format": "yaml"})
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "format must be 'text' or 'json'")
}

// TestDescribeTool_OpHelp_SetSignature covers the primary per-op
// help path: name=patch_function.set_signature must return a
// description mentioning both 'params' and 'returns'.
func TestDescribeTool_OpHelp_SetSignature(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callDescribe(t, cs, map[string]any{"name": "patch_function.set_signature"})
	require.False(t, result.IsError)
	text := resultText(t, result)
	assert.NotEmpty(t, text)
	assert.Contains(t, text, "patch_function.set_signature (edit)")
	assert.Contains(t, text, "params")
	assert.Contains(t, text, "returns")
	assert.Contains(t, text, "example:")
}

// TestDescribeTool_OpHelp_UnknownOpErrors asserts a bogus op on a
// tool with an Ops map surfaces the "unknown op" error and lists the
// known ops for the agent to pick from.
func TestDescribeTool_OpHelp_UnknownOpErrors(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callDescribe(t, cs, map[string]any{"name": "patch_function.nonsense"})
	require.True(t, result.IsError)
	text := resultText(t, result)
	assert.Contains(t, text, "unknown op")
	assert.Contains(t, text, "patch_function")
	// Known ops must be listed so the agent sees the menu.
	assert.Contains(t, text, "set_signature")
}

// TestDescribeTool_OpHelp_NoOpsOnTool asserts tools that don't expose
// an ops map (e.g. overview) surface "has no ops" rather than silently
// falling through to an unknown-tool error.
func TestDescribeTool_OpHelp_NoOpsOnTool(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})
	result := callDescribe(t, cs, map[string]any{"name": "overview.bar"})
	require.True(t, result.IsError)
	text := resultText(t, result)
	assert.Contains(t, text, "has no ops")
	assert.Contains(t, text, "overview")
}
