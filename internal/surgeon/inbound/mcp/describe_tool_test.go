package mcp_test

import (
	"context"
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
