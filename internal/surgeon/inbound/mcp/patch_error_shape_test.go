package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertCanonicalErrorShape verifies that an error result from a tool call
// surfaces the documented {"code":"...","message":"..."} StructuredContent
// shape (issue #12). The wire-decoded StructuredContent must be a JSON object
// with exactly two string fields ("code" and "message"), and the inner
// message must be the raw text — never a JSON-encoded string nested under a
// legacy "error" key.
func assertCanonicalErrorShape(t *testing.T, result *mcp.CallToolResult, wantCode string) {
	t.Helper()
	require.True(t, result.IsError, "expected IsError=true on error result")
	require.NotNil(t, result.StructuredContent, "expected StructuredContent on error result")

	// The in-memory transport JSON-roundtrips the result, so any concrete
	// type set by the handler arrives as map[string]any here. That is the
	// exact shape an MCP client receives over the wire.
	m, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok, "expected StructuredContent to decode as a JSON object, got %T", result.StructuredContent)

	// No legacy "error" wrapper key.
	_, hasLegacyError := m["error"]
	assert.Falsef(t, hasLegacyError, "StructuredContent must not carry a legacy \"error\" key (issue #12); got %v", m)

	// Required canonical keys.
	code, codeOK := m["code"].(string)
	require.Truef(t, codeOK, "StructuredContent must have string \"code\"; got %v", m)
	msg, msgOK := m["message"].(string)
	require.Truef(t, msgOK, "StructuredContent must have string \"message\"; got %v", m)

	assert.Equal(t, wantCode, code, "code mismatch")
	assert.NotEmpty(t, msg, "message must not be empty")

	// The message must NOT itself be a JSON-encoded string (the original bug
	// double-encoded the message, so it arrived as `"\"…\""`).
	assert.NotEqualf(t, byte('"'), msg[0],
		"message must be plain text, not a JSON-encoded string; got %q", msg)

	// Round-trip the StructuredContent through json.Marshal to assert no
	// surprise top-level keys. The canonical shape is exactly two fields.
	buf, err := json.Marshal(m)
	require.NoError(t, err)
	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(buf, &decoded))
	for k := range decoded {
		assert.Containsf(t, []string{"code", "message"}, k,
			"unexpected top-level key %q in error StructuredContent: %s", k, string(buf))
	}
}

// TestPatchFunction_ErrorShape_Issue12 verifies that target=function returns
// the canonical errorOutput shape when the underlying command fails.
func TestPatchFunction_ErrorShape_Issue12(t *testing.T) {
	commands := &mockCommands{
		patchFunctionFn: func(_ context.Context, _ domain.PatchFunctionRequest) (domain.PatchFunctionResult, error) {
			return domain.PatchFunctionResult{}, &domain.Error{
				Code:    "PATCH_FAILED",
				Message: "match \"foo\" not found",
			}
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target":     "function",
		"file":       "foo.go",
		"identifier": "Foo",
		"patches": []map[string]any{
			{"op": "replace", "match": "foo", "replace": "bar"},
		},
	})
	assertCanonicalErrorShape(t, result, "PATCH_FAILED")
}

// TestPatchStruct_ErrorShape_Issue12 covers target=struct.
func TestPatchStruct_ErrorShape_Issue12(t *testing.T) {
	commands := &mockCommands{
		patchStructFn: func(_ context.Context, _ domain.PatchStructRequest) (domain.PatchStructResult, error) {
			return domain.PatchStructResult{}, &domain.Error{
				Code:    "NOT_FOUND",
				Message: "field \"X\" not found",
			}
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target":     "struct",
		"file":       "foo.go",
		"identifier": "Foo",
		"patches": []map[string]any{
			{"op": "remove_field", "name": "X"},
		},
	})
	assertCanonicalErrorShape(t, result, "NOT_FOUND")
}

// TestPatchInterface_ErrorShape_Issue12 covers target=interface.
func TestPatchInterface_ErrorShape_Issue12(t *testing.T) {
	commands := &mockCommands{
		patchInterfaceFn: func(_ context.Context, _ domain.PatchInterfaceRequest) (domain.PatchInterfaceResult, error) {
			return domain.PatchInterfaceResult{}, &domain.Error{
				Code:    "NODE_NOT_FOUND",
				Message: "interface \"Foo\" not found",
			}
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target":     "interface",
		"file":       "foo.go",
		"identifier": "Foo",
		"patches": []map[string]any{
			{"op": "remove_method", "name": "X"},
		},
	})
	assertCanonicalErrorShape(t, result, "NODE_NOT_FOUND")
}

// TestPatchFile_ErrorShape_Issue12 covers target=file.
func TestPatchFile_ErrorShape_Issue12(t *testing.T) {
	commands := &mockCommands{
		patchFileFn: func(_ context.Context, _ domain.PatchFileRequest) (domain.PatchFileResult, error) {
			return domain.PatchFileResult{}, &domain.Error{
				Code:    "PATCH_FAILED",
				Message: "match \"foo\" not found in file",
			}
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target": "file",
		"file":   "foo.go",
		"patches": []map[string]any{
			{"match": "foo", "replace": "bar"},
		},
	})
	assertCanonicalErrorShape(t, result, "PATCH_FAILED")
}

// TestPatchDecl_ErrorShape_Issue12 covers target=decl.
func TestPatchDecl_ErrorShape_Issue12(t *testing.T) {
	commands := &mockCommands{
		patchDeclFn: func(_ context.Context, _ domain.PatchDeclRequest) (domain.PatchDeclResult, error) {
			return domain.PatchDeclResult{}, &domain.Error{
				Code:    "NODE_NOT_FOUND",
				Message: "decl \"foo\" not found",
			}
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target":     "decl",
		"file":       "foo.go",
		"identifier": "foo",
		"patches": []map[string]any{
			{"op": "replace", "match": "x", "replace": "y"},
		},
	})
	assertCanonicalErrorShape(t, result, "NODE_NOT_FOUND")
}

// TestPatch_PlainErrorPath_ErrorShape_Issue12 covers an error path that goes
// through errorResult (not errorResultWithCode) — the unknown-target branch
// in registerPatchTool. The shape contract must hold for every error path.
func TestPatch_PlainErrorPath_ErrorShape_Issue12(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target": "bogus",
		"file":   "foo.go",
		"patches": []map[string]any{
			{"op": "replace", "match": "x", "replace": "y"},
		},
	})
	assertCanonicalErrorShape(t, result, "ERROR")
}

// TestPatch_SuccessShape_Unaffected_Issue12 ensures the fix to the error
// shape did not regress the success-path StructuredContent (which carries the
// patchOutput payload, not errorOutput).
func TestPatch_SuccessShape_Unaffected_Issue12(t *testing.T) {
	commands := &mockCommands{
		patchFunctionFn: func(_ context.Context, _ domain.PatchFunctionRequest) (domain.PatchFunctionResult, error) {
			return domain.PatchFunctionResult{Applied: 1, Diff: "diff"}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target":     "function",
		"file":       "foo.go",
		"identifier": "Foo",
		"patches": []map[string]any{
			{"op": "replace", "match": "foo", "replace": "bar"},
		},
	})
	require.False(t, result.IsError, resultText(t, result))
	require.NotNil(t, result.StructuredContent)
	m, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok, "expected JSON object StructuredContent on success")
	// Success payload uses patchOutput keys, NOT errorOutput keys.
	_, hasCode := m["code"]
	assert.False(t, hasCode, "success StructuredContent must not carry an error \"code\" field")
	assert.Equal(t, "foo.go", m["file"])
	assert.Equal(t, float64(1), m["applied"])
}

// TestPatch_SchemaHintMiddlewarePath_ErrorShape_Issue12 covers the
// schema_hint middleware path. When 'patches' arrives as a JSON-encoded
// string instead of an array, the middleware short-circuits BEFORE the
// SDK validator, returning a CallToolResult directly. That direct path
// historically forgot to populate StructuredContent, so agents lost the
// canonical {code, message} shape and saw only the legacy text content
// (often re-wrapped by harnesses as {"error":"..."} — issue #12).
func TestPatch_SchemaHintMiddlewarePath_ErrorShape_Issue12(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "patch",
		Arguments: map[string]any{
			"target":     "function",
			"file":       "foo.go",
			"identifier": "Foo",
			// patches double-encoded as a JSON string — triggers the middleware.
			// patches as an unrelated non-array string — still rejected by the middleware
			// (recovery only applies when the inner value parses as a JSON array).
			"patches": "not-json",
		},
	})
	require.NoError(t, err)
	assertCanonicalErrorShape(t, result, "INVALID_ARGUMENT")
}

// TestPatch_SchemaHintFieldTypePath_ErrorShape_Issue12 covers the second
// middleware short-circuit: a patch op whose 'replace' field arrived as
// a JSON array instead of a string (the silent-data-loss pattern from
// issue #3). Same canonical shape contract.
func TestPatch_SchemaHintFieldTypePath_ErrorShape_Issue12(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "patch",
		Arguments: map[string]any{
			"target":     "function",
			"file":       "foo.go",
			"identifier": "Foo",
			"patches": []map[string]any{
				// 'replace' is an array of strings instead of a string.
				{"op": "replace", "match": "x", "replace": []string{"line1", "line2"}},
			},
		},
	})
	require.NoError(t, err)
	assertCanonicalErrorShape(t, result, "INVALID_ARGUMENT")
}
