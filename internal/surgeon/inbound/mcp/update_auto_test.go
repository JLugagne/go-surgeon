package mcp_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureExecutePlan returns a mockCommands whose ExecutePlan records the
// plan it receives, so routing decisions made by the MCP layer can be
// asserted without touching a real filesystem.
func captureExecutePlan(got *domain.Plan) *mockCommands {
	return &mockCommands{executePlanFn: func(_ context.Context, plan domain.Plan) (domain.PlanResult, error) {
		*got = plan
		return domain.PlanResult{FilesModified: 1, Files: []string{plan.Actions[0].FilePath}}, nil
	}}
}

// TestUpdate_AutoClassification asserts that update object=auto (the default)
// routes content to the action type of its single declaration. Content that
// starts with a doc comment, a single-line struct (no space before the
// brace), an interface, or a bare type declaration must NOT fall back to
// replace_file: that fallback rewrites the entire target file and silently
// destroys every other declaration in it.
func TestUpdate_AutoClassification(t *testing.T) {
	cases := []struct {
		name       string
		identifier string
		content    string
		want       domain.ActionType
	}{
		{"plain func", "Foo", "func Foo() error {\n\treturn nil\n}", domain.ActionTypeUpdateFunc},
		{"doc comment then func", "Book.Validate", "// Validate checks invariants.\nfunc (b *Book) Validate() error {\n\treturn nil\n}", domain.ActionTypeUpdateFunc},
		{"doc comment then method with blank line", "Book.Save", "// Save persists the book.\n//\n// It returns an error on conflict.\nfunc (b *Book) Save() error {\n\treturn nil\n}", domain.ActionTypeUpdateFunc},
		{"multi-line struct", "Point", "type Point struct {\n\tX int\n}", domain.ActionTypeUpdateStruct},
		{"single-line struct no space", "Point", "type Point struct{ X, Y int }", domain.ActionTypeUpdateStruct},
		{"doc comment then struct", "Point", "// Point is a 2D point.\ntype Point struct{ X, Y int }", domain.ActionTypeUpdateStruct},
		{"interface", "Repo", "type Repo interface {\n\tGet(id string) error\n}", domain.ActionTypeUpdateInterface},
		{"type alias", "UserID", "type UserID string", domain.ActionTypeUpdateDecl},
		{"const", "maxRetries", "const maxRetries = 3", domain.ActionTypeUpdateDecl},
		{"var", "debug", "var debug = false", domain.ActionTypeUpdateDecl},
		{"full file with package clause", "", "package book\n\nfunc A() {}\n\nfunc B() {}\n", domain.ActionTypeReplaceFile},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got domain.Plan
			cs := setupTest(t, captureExecutePlan(&got), &mockQueries{})

			result := callTool(t, cs, "update", map[string]any{
				"file":       "book.go",
				"identifier": tc.identifier,
				"content":    tc.content,
			})
			require.False(t, result.IsError, resultText(t, result))
			require.Len(t, got.Actions, 1)
			assert.Equal(t, tc.want, got.Actions[0].Action)
			assert.Equal(t, tc.content, got.Actions[0].Content, "content must pass through unmodified")
		})
	}
}

// TestUpdate_AutoRejectsAmbiguousContent asserts that content auto cannot
// classify produces an actionable error instead of a silent whole-file
// replacement (the previous fallback).
func TestUpdate_AutoRejectsAmbiguousContent(t *testing.T) {
	cases := []struct {
		name            string
		content         string
		wantErrContains string
	}{
		{"unparseable content", "this is not go code {{{", "not parseable"},
		{"comment only", "// just a comment\n", "not parseable"},
		{"multiple declarations", "func A() {}\n\nfunc B() {}", "multiple declarations"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			commands := &mockCommands{executePlanFn: func(_ context.Context, _ domain.Plan) (domain.PlanResult, error) {
				called = true
				return domain.PlanResult{}, nil
			}}
			cs := setupTest(t, commands, &mockQueries{})

			result := callTool(t, cs, "update", map[string]any{
				"file":    "book.go",
				"content": tc.content,
			})
			require.True(t, result.IsError, "expected an error, got: %s", resultText(t, result))
			assert.Contains(t, resultText(t, result), tc.wantErrContains)
			assert.False(t, called, "ambiguous content must never reach ExecutePlan")
		})
	}
}

// TestCreate_AutoClassification asserts the create tool's object=auto
// sniffing handles doc comments and single-line structs; anything else keeps
// the historical create_file fallback (safe for create: an existing target
// file makes create_file fail instead of destroying data).
func TestCreate_AutoClassification(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    domain.ActionType
	}{
		{"plain func", "func New() int {\n\treturn 1\n}", domain.ActionTypeAddFunc},
		{"doc comment then func", "// New builds a thing.\nfunc New() int {\n\treturn 1\n}", domain.ActionTypeAddFunc},
		{"single-line struct no space", "type Pair struct{ A, B int }", domain.ActionTypeAddStruct},
		{"doc comment then struct", "// Pair holds two ints.\ntype Pair struct{ A, B int }", domain.ActionTypeAddStruct},
		{"full file content", "package pair\n\ntype Pair struct{ A, B int }\n", domain.ActionTypeCreateFile},
		{"const falls back to file", "const answer = 42", domain.ActionTypeCreateFile},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got domain.Plan
			cs := setupTest(t, captureExecutePlan(&got), &mockQueries{})

			result := callTool(t, cs, "create", map[string]any{
				"object":  "auto",
				"file":    "pair.go",
				"content": tc.content,
			})
			require.False(t, result.IsError, resultText(t, result))
			require.Len(t, got.Actions, 1)
			assert.Equal(t, tc.want, got.Actions[0].Action)
		})
	}
}
