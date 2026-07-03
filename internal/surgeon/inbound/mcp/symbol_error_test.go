package mcp_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSymbol_QueryError_Surfaced asserts that when every underlying
// FindSymbols call fails, the symbol tool reports the failure instead of
// the misleading "No matches found" success text (which sends agents off
// to refine a query that was never executed).
func TestSymbol_QueryError_Surfaced(t *testing.T) {
	queries := &mockQueries{findSymbolsFn: func(_ context.Context, _ domain.SymbolQuery, _ string) ([]domain.SymbolResult, error) {
		return nil, &domain.Error{Code: "INVALID_ARGUMENT", Message: "directory does not exist: internal/nope"}
	}}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "symbol", map[string]any{"query": "Foo", "dir": "internal/nope"})
	require.True(t, result.IsError, "a failing query must not masquerade as 'no matches': %s", resultText(t, result))
	assert.Contains(t, resultText(t, result), "directory does not exist")
}

// TestSymbol_QueryError_ResultsStillWin asserts the error is only surfaced
// when no query form produced results: with a two-part query, one form may
// legitimately fail while the other resolves.
func TestSymbol_QueryError_ResultsStillWin(t *testing.T) {
	queries := &mockQueries{findSymbolsFn: func(_ context.Context, q domain.SymbolQuery, _ string) ([]domain.SymbolResult, error) {
		if q.Receiver != "" {
			return []domain.SymbolResult{{Name: q.Name, Receiver: q.Receiver, Signature: "func (b Book) Validate() error", File: "book.go", LineStart: 3, LineEnd: 5}}, nil
		}
		return nil, &domain.Error{Code: "INTERNAL_ERROR", Message: "package load failed"}
	}}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "symbol", map[string]any{"query": "Book.Validate"})
	require.False(t, result.IsError, resultText(t, result))
	assert.Contains(t, resultText(t, result), "Validate")
}
