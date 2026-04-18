package mcp_test

import (
	"context"
	"errors"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBatchQuery_ThreeSymbolQueries covers the core use case:
// agents issuing N symbol reads in one turn to save round-trips.
func TestBatchQuery_ThreeSymbolQueries(t *testing.T) {
	queries := &mockQueries{
		findSymbolsFn: func(_ context.Context, query domain.SymbolQuery, _ string) ([]domain.SymbolResult, error) {
			switch query.Name {
			case "NewBook":
				return []domain.SymbolResult{{
					Name: "NewBook", File: "book.go", LineStart: 10, LineEnd: 15,
					Signature: "func NewBook(title string) *Book",
				}}, nil
			case "NewAuthor":
				return []domain.SymbolResult{{
					Name: "NewAuthor", File: "author.go", LineStart: 20, LineEnd: 25,
					Signature: "func NewAuthor(name string) *Author",
				}}, nil
			case "NewShelf":
				return []domain.SymbolResult{{
					Name: "NewShelf", File: "shelf.go", LineStart: 30, LineEnd: 35,
					Signature: "func NewShelf() *Shelf",
				}}, nil
			}
			return nil, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "batch_query", map[string]any{
		"queries": []map[string]any{
			{"op": "symbol", "query": "NewBook"},
			{"op": "symbol", "query": "NewAuthor"},
			{"op": "symbol", "query": "NewShelf"},
		},
	})

	assert.False(t, result.IsError)
	text := resultText(t, result)
	assert.Contains(t, text, "=== [0] symbol ===")
	assert.Contains(t, text, "NewBook")
	assert.Contains(t, text, "=== [1] symbol ===")
	assert.Contains(t, text, "NewAuthor")
	assert.Contains(t, text, "=== [2] symbol ===")
	assert.Contains(t, text, "NewShelf")

	require.NotNil(t, result.StructuredContent)
}

// TestBatchQuery_MixedOps covers the cross-tool investigation pattern:
// one overview call + one symbol + one find_references, combined in a
// single round-trip.
func TestBatchQuery_MixedOps(t *testing.T) {
	queries := &mockQueries{
		graphFn: func(_ context.Context, opts domain.GraphOptions) ([]domain.GraphPackage, error) {
			assert.Equal(t, "internal/domain", opts.Focus)
			return []domain.GraphPackage{{Path: "internal/domain"}}, nil
		},
		findSymbolsFn: func(_ context.Context, query domain.SymbolQuery, _ string) ([]domain.SymbolResult, error) {
			if query.Name == "Validate" {
				return []domain.SymbolResult{{
					Name: "Validate", File: "book.go", LineStart: 40, LineEnd: 42,
					Signature: "func Validate(b *Book) error",
				}}, nil
			}
			return nil, nil
		},
		findReferencesFn: func(_ context.Context, q domain.ReferencesQuery) (domain.ReferencesResult, error) {
			assert.Equal(t, "Validate", q.Symbol.Name)
			return domain.ReferencesResult{
				Symbol: q.Symbol,
				Kind:   "func",
				References: []domain.Location{
					{File: "main.go", Line: 10, Column: 5},
					{File: "handler.go", Line: 20, Column: 3},
				},
			}, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "batch_query", map[string]any{
		"queries": []map[string]any{
			{"op": "overview", "focus": "internal/domain"},
			{"op": "symbol", "query": "Validate"},
			{"op": "find_references", "name": "Validate"},
		},
	})

	assert.False(t, result.IsError)
	text := resultText(t, result)
	assert.Contains(t, text, "=== [0] overview ===")
	assert.Contains(t, text, "internal/domain")
	assert.Contains(t, text, "=== [1] symbol ===")
	assert.Contains(t, text, "Symbol: Validate")
	assert.Contains(t, text, "=== [2] find_references ===")
	assert.Contains(t, text, "2 reference(s)")
	assert.Contains(t, text, "main.go:10:5")
}

// TestBatchQuery_FailSoft verifies that a single failing sub-query is
// surfaced as an error entry while remaining items still return their
// results. This is the whole point of batch_query: one bad item can't
// abort the rest of the investigation. Uses find_references to trigger
// an explicit error (the symbol-exact path swallows loader errors as
// "no matches" — consistent with the standalone symbol tool).
func TestBatchQuery_FailSoft(t *testing.T) {
	queries := &mockQueries{
		findSymbolsFn: func(_ context.Context, query domain.SymbolQuery, _ string) ([]domain.SymbolResult, error) {
			return []domain.SymbolResult{{
				Name: query.Name, File: "ok.go", LineStart: 1, LineEnd: 2,
				Signature: "func " + query.Name + "()",
			}}, nil
		},
		findReferencesFn: func(_ context.Context, q domain.ReferencesQuery) (domain.ReferencesResult, error) {
			if q.Symbol.Name == "Explodes" {
				return domain.ReferencesResult{}, errors.New("boom")
			}
			return domain.ReferencesResult{}, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "batch_query", map[string]any{
		"queries": []map[string]any{
			{"op": "symbol", "query": "GoodOne"},
			{"op": "find_references", "name": "Explodes"},
			{"op": "symbol", "query": "GoodTwo"},
		},
	})

	// Overall call does not hard-error — fail-soft returns per-item errors.
	assert.False(t, result.IsError)
	text := resultText(t, result)
	assert.Contains(t, text, "=== [0] symbol ===")
	assert.Contains(t, text, "GoodOne")
	assert.Contains(t, text, "=== [1] find_references ===")
	assert.Contains(t, text, "ERROR: boom")
	assert.Contains(t, text, "=== [2] symbol ===")
	assert.Contains(t, text, "GoodTwo")

	require.NotNil(t, result.StructuredContent)
}

// TestBatchQuery_EmptyRejected guards against empty batches — these are
// always a client bug and returning an explicit error is clearer than
// a silent empty success.
func TestBatchQuery_EmptyRejected(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	result := callTool(t, cs, "batch_query", map[string]any{
		"queries": []map[string]any{},
	})

	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "queries is required")
}

// TestBatchQuery_TooManyRejected verifies the 10-item cap.
func TestBatchQuery_TooManyRejected(t *testing.T) {
	cs := setupTest(t, &mockCommands{}, &mockQueries{})

	items := make([]map[string]any, 0, 11)
	for i := 0; i < 11; i++ {
		items = append(items, map[string]any{"op": "symbol", "query": "X"})
	}
	result := callTool(t, cs, "batch_query", map[string]any{"queries": items})

	assert.True(t, result.IsError)
	assert.Contains(t, resultText(t, result), "too many sub-queries")
}

// TestBatchQuery_UnknownOp verifies an invalid op errors the item
// without aborting the batch.
func TestBatchQuery_UnknownOp(t *testing.T) {
	queries := &mockQueries{
		findSymbolsFn: func(_ context.Context, query domain.SymbolQuery, _ string) ([]domain.SymbolResult, error) {
			return []domain.SymbolResult{{
				Name: query.Name, File: "ok.go", LineStart: 1, LineEnd: 2,
				Signature: "func " + query.Name + "()",
			}}, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "batch_query", map[string]any{
		"queries": []map[string]any{
			{"op": "not_a_real_op"},
			{"op": "symbol", "query": "StillWorks"},
		},
	})

	assert.False(t, result.IsError)
	text := resultText(t, result)
	assert.Contains(t, text, "ERROR: unknown op")
	assert.Contains(t, text, "StillWorks")
}
