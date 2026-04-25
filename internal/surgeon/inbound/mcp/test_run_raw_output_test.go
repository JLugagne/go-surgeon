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

func decodeTestRunStructured(t *testing.T, result *mcp.CallToolResult) domain.TestRunResult {
	t.Helper()
	require.NotNil(t, result.StructuredContent)
	data, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var decoded domain.TestRunResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	return decoded
}

func TestTestRun_SuccessOmitsRawOutputByDefault(t *testing.T) {
	queries := &mockQueries{
		testRunFn: func(_ context.Context, _ domain.TestRunRequest) (domain.TestRunResult, error) {
			return domain.TestRunResult{
				Success:    true,
				Summary:    "10 passed, 0 failed in 1 package (0.1s)",
				RawOutput:  `{"Action":"pass","Package":"x"}` + "\n",
				DurationMS: 100,
			}, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "test_run", map[string]any{})
	decoded := decodeTestRunStructured(t, result)
	assert.True(t, decoded.Success)
	assert.Empty(t, decoded.RawOutput, "raw_output should be dropped on success when include_raw_output is unset")
}

func TestTestRun_SuccessKeepsRawOutputWhenOptedIn(t *testing.T) {
	rawStream := `{"Action":"pass","Package":"x"}` + "\n"
	queries := &mockQueries{
		testRunFn: func(_ context.Context, _ domain.TestRunRequest) (domain.TestRunResult, error) {
			return domain.TestRunResult{
				Success:   true,
				Summary:   "10 passed",
				RawOutput: rawStream,
			}, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "test_run", map[string]any{"include_raw_output": true})
	decoded := decodeTestRunStructured(t, result)
	assert.True(t, decoded.Success)
	assert.Equal(t, rawStream, decoded.RawOutput, "raw_output must be preserved when include_raw_output=true")
}

func TestTestRun_FailureKeepsRawOutput(t *testing.T) {
	rawStream := `{"Action":"fail","Package":"x"}` + "\n"
	queries := &mockQueries{
		testRunFn: func(_ context.Context, _ domain.TestRunRequest) (domain.TestRunResult, error) {
			return domain.TestRunResult{
				Success:   false,
				Summary:   "0 passed, 1 failed",
				RawOutput: rawStream,
			}, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "test_run", map[string]any{})
	decoded := decodeTestRunStructured(t, result)
	assert.False(t, decoded.Success)
	assert.Equal(t, rawStream, decoded.RawOutput, "raw_output must always be preserved on failure")
}
