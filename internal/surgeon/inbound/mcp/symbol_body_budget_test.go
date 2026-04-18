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

// buildManyBodyResults returns n SymbolResults with enough Code bytes
// per entry (~250 bytes each) that a small token_budget will trip the
// degradation path within the first few entries.
func buildManyBodyResults(n int) []domain.SymbolResult {
	out := make([]domain.SymbolResult, 0, n)
	body := strings.Repeat("  // filler line with enough bytes to matter\n", 6)
	for i := 0; i < n; i++ {
		name := "Fn" + string(rune('0'+i%10))
		out = append(out, domain.SymbolResult{
			Name:      name,
			File:      "file.go",
			LineStart: i * 10,
			Signature: "func " + name + "()",
			Code:      body,
		})
	}
	return out
}

func callSymbolPattern(t *testing.T, cs *mcp.ClientSession, pattern string, body bool, budget int) *mcp.CallToolResult {
	t.Helper()
	args := map[string]any{
		"pattern": pattern,
		"body":    body,
	}
	if budget != 0 {
		args["token_budget"] = budget
	}
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "symbol",
		Arguments: args,
	})
	require.NoError(t, err)
	return result
}

// TestSymbolPattern_BodyManyMatches_NoHardCap removes the old
// "body=true refused at >3 matches" behavior: unlimited budget must
// now emit every body.
func TestSymbolPattern_BodyManyMatches_NoHardCap(t *testing.T) {
	queries := &mockQueries{
		findSymbolsFn: func(ctx context.Context, q domain.SymbolQuery, dir string) ([]domain.SymbolResult, error) {
			return buildManyBodyResults(6), nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callSymbolPattern(t, cs, "^Fn", true, 0)
	require.False(t, result.IsError, "unexpected error: %s", resultText(t, result))
	text := resultText(t, result)
	assert.NotContains(t, text, "budget reached")
	assert.NotContains(t, text, "body=true refused")
	assert.Contains(t, text, "filler line with enough bytes to matter")
}

// TestSymbolPattern_BodyTightBudget_DegradesToSignatures asserts that
// once the running output exceeds token_budget*4 bytes, remaining
// results come through as signature-only with a trailer.
func TestSymbolPattern_BodyTightBudget_DegradesToSignatures(t *testing.T) {
	queries := &mockQueries{
		findSymbolsFn: func(ctx context.Context, q domain.SymbolQuery, dir string) ([]domain.SymbolResult, error) {
			return buildManyBodyResults(6), nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	// Budget of 200 tokens ≈ 800 bytes — enough for a couple of
	// full body blocks but well short of all 6.
	result := callSymbolPattern(t, cs, "^Fn", true, 200)
	require.False(t, result.IsError, "unexpected error: %s", resultText(t, result))
	text := resultText(t, result)
	assert.Contains(t, text, "budget reached after")
	assert.Contains(t, text, "signatures only")
}

// TestSymbolPattern_BodyZeroBudget_IsUnlimited confirms that the zero
// sentinel matches the "omitted" convention used by overview and the
// existing pattern-mode truncation: zero means no cap.
func TestSymbolPattern_BodyZeroBudget_IsUnlimited(t *testing.T) {
	queries := &mockQueries{
		findSymbolsFn: func(ctx context.Context, q domain.SymbolQuery, dir string) ([]domain.SymbolResult, error) {
			return buildManyBodyResults(6), nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callSymbolPattern(t, cs, "^Fn", true, 0)
	require.False(t, result.IsError)
	text := resultText(t, result)
	assert.NotContains(t, text, "budget reached")
}
