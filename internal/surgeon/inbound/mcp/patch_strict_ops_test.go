package mcp_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPatch_UnknownOpField_Function asserts that a typo'd field name inside
// a patch op is rejected with an actionable message instead of being
// silently dropped (a dropped field silently changes the op's targeting).
func TestPatch_UnknownOpField_Function(t *testing.T) {
	called := false
	commands := &mockCommands{patchFunctionFn: func(_ context.Context, _ domain.PatchFunctionRequest) (domain.PatchFunctionResult, error) {
		called = true
		return domain.PatchFunctionResult{Applied: 1}, nil
	}}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target":     "function",
		"file":       "foo.go",
		"identifier": "Foo",
		"patches": []map[string]any{
			{"op": "replace", "match": "x", "replce": "y"},
		},
	})
	require.True(t, result.IsError, "typo'd op field must be rejected: %s", resultText(t, result))
	assert.Contains(t, resultText(t, result), "replce")
	assert.False(t, called, "invalid ops must not reach the handler")
}

// TestPatch_UnknownOpField_File covers the same protection on the file
// target's op shape.
func TestPatch_UnknownOpField_File(t *testing.T) {
	called := false
	commands := &mockCommands{patchFileFn: func(_ context.Context, _ domain.PatchFileRequest) (domain.PatchFileResult, error) {
		called = true
		return domain.PatchFileResult{Applied: 1}, nil
	}}
	cs := setupTest(t, commands, &mockQueries{})

	result := callTool(t, cs, "patch", map[string]any{
		"target": "file",
		"file":   "foo.go",
		"patches": []map[string]any{
			{"mach": "old", "replace": "new"},
		},
	})
	require.True(t, result.IsError, "typo'd op field must be rejected: %s", resultText(t, result))
	assert.Contains(t, resultText(t, result), "mach")
	assert.False(t, called, "invalid ops must not reach the handler")
}
