package mcp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSchemaHint_PatchFunctionStringPatches verifies that when a client
// serializes 'patches' as a JSON string instead of an array, the middleware
// intercepts before SDK schema validation and returns an actionable message
// naming the root cause ("JSON-encoded string instead of an array"), rather
// than the opaque default validator error.
func TestSchemaHint_PatchFunctionStringPatches(t *testing.T) {
	var called bool
	commands := &mockCommands{
		patchFunctionFn: func(_ context.Context, _ domain.PatchFunctionRequest) (domain.PatchFunctionResult, error) {
			called = true
			return domain.PatchFunctionResult{}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "patch",
		Arguments: map[string]any{
			"target":     "function",
			"file":       "foo.go",
			"identifier": "Foo",
			"patches":    `[{"op":"replace","match":"x","replace":"y"}]`,
		},
	})
	require.NoError(t, err)
	require.True(t, result.IsError, "expected IsError=true")
	text := resultText(t, result)
	assert.Contains(t, text, "JSON-encoded string instead of an array")
	assert.Contains(t, text, "serialized twice", "inner value parses as array → mention double-serialization")
	assert.False(t, called, "business handler must not run when the middleware rejects")
}

// TestSchemaHint_PatchStructStringPatches covers patch_struct — any patch tool
// with a 'patches' field must go through the same middleware.
func TestSchemaHint_PatchStructStringPatches(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "patch",
		Arguments: map[string]any{
			"target":     "struct",
			"file":       "foo.go",
			"identifier": "Foo",
			"patches":    `[{"op":"add_field","name":"X","type":"int"}]`,
		},
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "JSON-encoded string instead of an array")
}

// TestSchemaHint_PatchInterfaceStringPatches covers patch_interface.
func TestSchemaHint_PatchInterfaceStringPatches(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "patch",
		Arguments: map[string]any{
			"target":     "interface",
			"file":       "foo.go",
			"identifier": "Foo",
			"patches":    `[{"op":"add_method","signature":"Close() error"}]`,
		},
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "JSON-encoded string instead of an array")
}

// TestSchemaHint_PatchDeclStringPatches covers patch_decl.
func TestSchemaHint_PatchDeclStringPatches(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "patch",
		Arguments: map[string]any{
			"target":     "decl",
			"file":       "foo.go",
			"identifier": "Foo",
			"patches":    `[{"op":"replace","match":"x","replace":"y"}]`,
		},
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "JSON-encoded string instead of an array")
}

// TestSchemaHint_ArrayPatchesPassThrough verifies that the middleware does not
// trigger on well-formed array 'patches' — it should be a no-op in the happy
// path. We send a valid patch_function call and expect it to reach the business
// handler.
func TestSchemaHint_ArrayPatchesPassThrough(t *testing.T) {
	var received domain.PatchFunctionRequest
	commands := &mockCommands{
		patchFunctionFn: func(_ context.Context, req domain.PatchFunctionRequest) (domain.PatchFunctionResult, error) {
			received = req
			return domain.PatchFunctionResult{Applied: 1, Diff: "mock diff"}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "patch",
		Arguments: map[string]any{
			"target":     "function",
			"file":       "foo.go",
			"identifier": "Foo",
			"patches": []map[string]any{
				{"op": "replace", "match": "x", "replace": "y"},
			},
		},
	})
	require.NoError(t, err)
	require.False(t, result.IsError, "well-formed array must not trigger the hint: %s", resultText(t, result))
	assert.Equal(t, "foo.go", received.FilePath)
	assert.Len(t, received.Patches, 1)
}

// TestSchemaHint_NonPatchTool confirms the middleware only targets patch tools
// and leaves unrelated tools alone.
func TestSchemaHint_NonPatchTool(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	// 'create' has no 'patches' field. Sending a malformed string for 'content'
	// shouldn't trigger the patches hint — it should go to the SDK's own
	// validation (or succeed, depending on the schema).
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create",
		Arguments: map[string]any{
			"object":  "func",
			"file":    "foo.go",
			"content": "func X() {}",
		},
	})
	require.NoError(t, err)
	// Whatever the outcome, it must NOT carry the patches-mismatch wording.
	if result != nil {
		assert.NotContains(t, resultText(t, result), "JSON-encoded string instead of an array")
	}
}

// TestSchemaHint_PatchesIsUnrelatedString guards against false positives: if
// a client sends patches as a plain string that does NOT decode to an array
// (e.g. a human-readable label), we should still flag it, but without the
// "serialized twice" wording.
func TestSchemaHint_PatchesIsUnrelatedString(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "patch",
		Arguments: map[string]any{
			"target":     "function",
			"file":       "foo.go",
			"identifier": "Foo",
			"patches":    "not-json",
		},
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	text := resultText(t, result)
	assert.Contains(t, text, "JSON-encoded string instead of an array")
	assert.False(t, strings.Contains(text, "serialized twice"), "inner value is not a JSON array, should not claim double-serialization")
}

// TestSchemaHint_PatchOpReplaceAsArray verifies that a patch op whose
// 'replace' field arrived as a JSON array (the silent-data-loss pattern
// described in issue #3) is intercepted before the splice ever runs.
// The middleware must return an actionable message naming the offending
// patch index and field, and the business handler must not be invoked.
func TestSchemaHint_PatchOpReplaceAsArray(t *testing.T) {
	var called bool
	commands := &mockCommands{
		patchFunctionFn: func(_ context.Context, _ domain.PatchFunctionRequest) (domain.PatchFunctionResult, error) {
			called = true
			return domain.PatchFunctionResult{}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "patch",
		Arguments: map[string]any{
			"target":     "function",
			"file":       "foo.go",
			"identifier": "Foo",
			"patches": []map[string]any{
				{
					"op":      "replace",
					"match":   "x",
					"replace": []string{"line1", "line2"},
				},
			},
		},
	})
	require.NoError(t, err)
	require.True(t, result.IsError, "expected the middleware to flag the bad type")
	text := resultText(t, result)
	assert.Contains(t, text, "patch #1")
	assert.Contains(t, text, "replace")
	assert.Contains(t, text, "JSON array")
	assert.Contains(t, text, "string is required")
	assert.False(t, called, "business handler must not run when the middleware rejects")
}
