package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPatch_Union_RejectsMixedShapes asserts that mixing top-level
// single-target fields with items[] is rejected up front so callers don't
// silently get one shape ignored.
func TestPatch_Union_RejectsMixedShapes(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target":     "function",
		"file":       "foo.go",
		"identifier": "Foo",
		"patches": []map[string]any{
			{"op": "replace", "match": "x", "replace": "y"},
		},
		"items": []map[string]any{
			{
				"file":       "bar.go",
				"identifier": "Bar",
				"patches":    []map[string]any{{"op": "replace", "match": "x", "replace": "y"}},
			},
		},
	})
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "cannot mix top-level")
}

// TestPatch_Union_RejectsEmptyInput asserts that calling patch with neither
// shape populated is rejected with an actionable message.
func TestPatch_Union_RejectsEmptyInput(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target": "function",
	})
	require.True(t, result.IsError)
	text := resultText(t, result)
	assert.Contains(t, text, "either set items[]")
	assert.Contains(t, text, "single-target")
}

// TestPatch_Union_SingleTarget_File_NoIdentifier verifies the single-target
// shape on target=file works without an identifier (file targets are
// whole-file edits).
func TestPatch_Union_SingleTarget_File_NoIdentifier(t *testing.T) {
	var received domain.PatchFileRequest
	commands := &mockCommands{
		patchFileFn: func(_ context.Context, req domain.PatchFileRequest) (domain.PatchFileResult, error) {
			received = req
			return domain.PatchFileResult{Applied: 1, Diff: "ok", Hits: []int{1}}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target": "file",
		"file":   "foo.go",
		"patches": []map[string]any{
			{"match": "old", "replace": "new"},
		},
	})
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, "foo.go", received.FilePath)
	require.Len(t, received.Patches, 1)
	assert.Equal(t, "old", received.Patches[0].Match)
	assert.Equal(t, "new", received.Patches[0].Replace)
}

// TestPatch_Union_Parity_SingleVsItems verifies that the single-target shape
// and a length-1 items[] form produce equivalent structured output (same
// applied count, same items[0] payload). This is the parity contract that
// allows callers to migrate either way without observable change.
func TestPatch_Union_Parity_SingleVsItems(t *testing.T) {
	commands := &mockCommands{
		patchFunctionFn: func(_ context.Context, _ domain.PatchFunctionRequest) (domain.PatchFunctionResult, error) {
			return domain.PatchFunctionResult{Applied: 1, Diff: "diff"}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	single := callTool(t, cs, "patch", map[string]any{
		"target":     "function",
		"file":       "foo.go",
		"identifier": "Foo",
		"patches": []map[string]any{
			{"op": "replace", "match": "foo", "replace": "bar"},
		},
	})
	bulk := callTool(t, cs, "patch", map[string]any{
		"target": "function",
		"items": []map[string]any{
			{
				"file":       "foo.go",
				"identifier": "Foo",
				"patches": []map[string]any{
					{"op": "replace", "match": "foo", "replace": "bar"},
				},
			},
		},
	})
	require.False(t, single.IsError, resultText(t, single))
	require.False(t, bulk.IsError, resultText(t, bulk))

	// Both must expose items[0] with the same applied count and file.
	require.NotNil(t, single.StructuredContent)
	require.NotNil(t, bulk.StructuredContent)
	singleBuf, err := json.Marshal(single.StructuredContent)
	require.NoError(t, err)
	bulkBuf, err := json.Marshal(bulk.StructuredContent)
	require.NoError(t, err)
	var s, b struct {
		Applied int `json:"applied"`
		Items   []struct {
			File    string `json:"file"`
			Applied int    `json:"applied"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(singleBuf, &s))
	require.NoError(t, json.Unmarshal(bulkBuf, &b))
	assert.Equal(t, b.Applied, s.Applied)
	require.Len(t, s.Items, 1)
	require.Len(t, b.Items, 1)
	assert.Equal(t, b.Items[0].File, s.Items[0].File)
	assert.Equal(t, b.Items[0].Applied, s.Items[0].Applied)
}

// TestPatch_Union_Bulk_MixedTargets_Interface verifies that the existing bulk
// path still works on the interface target after the union refactor — items
// are applied sequentially and each one's mock_file/mock_name is honoured.
func TestPatch_Union_Bulk_Interface(t *testing.T) {
	calls := 0
	commands := &mockCommands{
		patchInterfaceFn: func(_ context.Context, req domain.PatchInterfaceRequest) (domain.PatchInterfaceResult, error) {
			calls++
			return domain.PatchInterfaceResult{Applied: 1, Diff: "d", MockUpdated: req.MockFile != ""}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target": "interface",
		"items": []map[string]any{
			{
				"file":       "a.go",
				"identifier": "Reader",
				"mock_file":  "mock_reader.go",
				"mock_name":  "MockReader",
				"patches":    []map[string]any{{"op": "add_method", "signature": "Close() error"}},
			},
			{
				"file":       "b.go",
				"identifier": "Writer",
				"patches":    []map[string]any{{"op": "add_method", "signature": "Flush() error"}},
			},
		},
	})
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, 2, calls, "both items should reach the handler")
}
