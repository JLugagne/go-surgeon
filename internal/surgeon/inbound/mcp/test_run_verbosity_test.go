package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeTestCases builds a synthetic suite of n passing tests, optionally
// followed by m failing tests so we can assert that summary mode keeps
// failures while collapsing passes.
func makeTestCases(passes, fails int) []domain.TestCaseResult {
	out := make([]domain.TestCaseResult, 0, passes+fails)
	for i := 0; i < passes; i++ {
		out = append(out, domain.TestCaseResult{
			Package:   "pkg",
			Name:      fmt.Sprintf("TestPass%d", i),
			Status:    "pass",
			ElapsedMS: 10 + i,
		})
	}
	for i := 0; i < fails; i++ {
		out = append(out, domain.TestCaseResult{
			Package:        "pkg",
			Name:           fmt.Sprintf("TestFail%d", i),
			Status:         "fail",
			ElapsedMS:      20 + i,
			FailureFile:    "pkg/foo_test.go",
			FailureLine:    42 + i,
			FailureMessage: "expected x got y",
		})
	}
	return out
}

func TestTestRun_VerbositySummary_DropsRawOutputAndPasses(t *testing.T) {
	queries := &mockQueries{
		testRunFn: func(_ context.Context, _ domain.TestRunRequest) (domain.TestRunResult, error) {
			return domain.TestRunResult{
				Success:    true,
				Tests:      makeTestCases(10, 0),
				Summary:    "10 passed, 0 failed",
				RawOutput:  `{"Action":"pass"}` + "\n",
				DurationMS: 100,
			}, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "test_run", map[string]any{"verbosity": "summary"})
	require.NotNil(t, result.StructuredContent)

	// The structured payload in summary mode is the compact shape (passing
	// tests are collapsed into a counter, raw_output is dropped). Decode as
	// a generic map to assert exactly what fields are present.
	data, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(data, &payload))

	assert.Equal(t, true, payload["success"])
	assert.Equal(t, "summary", payload["verbosity"])
	assert.Equal(t, float64(10), payload["passed_count"])
	assert.Empty(t, payload["raw_output"], "raw_output must be dropped in summary mode")
	tests, _ := payload["tests"].([]any)
	assert.Empty(t, tests, "passing tests should be collapsed into passed_count, not listed")
}

func TestTestRun_VerbosityFull_KeepsEverything(t *testing.T) {
	rawStream := `{"Action":"pass"}` + "\n"
	queries := &mockQueries{
		testRunFn: func(_ context.Context, _ domain.TestRunRequest) (domain.TestRunResult, error) {
			return domain.TestRunResult{
				Success:    true,
				Tests:      makeTestCases(3, 0),
				Summary:    "3 passed",
				RawOutput:  rawStream,
				DurationMS: 50,
			}, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	// verbosity=full bypasses both the raw_output drop-on-success rule and
	// the auto threshold; it must include every per-test field.
	result := callTool(t, cs, "test_run", map[string]any{
		"verbosity":          "full",
		"include_raw_output": true,
	})
	decoded := decodeTestRunStructured(t, result)
	assert.True(t, decoded.Success)
	assert.Equal(t, rawStream, decoded.RawOutput)
	require.Len(t, decoded.Tests, 3)
	assert.Equal(t, 10, decoded.Tests[0].ElapsedMS, "per-test elapsed_ms must be preserved in full mode")
}

func TestTestRun_VerbosityAuto_SummaryAboveThreshold(t *testing.T) {
	// 51 tests is one above the threshold (50) — auto mode should kick into
	// summary. We build all-passing tests so the surviving tests slice is
	// empty and passed_count carries the signal.
	queries := &mockQueries{
		testRunFn: func(_ context.Context, _ domain.TestRunRequest) (domain.TestRunResult, error) {
			return domain.TestRunResult{
				Success:    true,
				Tests:      makeTestCases(51, 0),
				Summary:    "51 passed, 0 failed",
				RawOutput:  `{"Action":"pass"}` + "\n",
				DurationMS: 1234,
			}, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "test_run", map[string]any{})
	data, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(data, &payload))

	assert.Equal(t, "summary", payload["verbosity"], "auto mode must pick summary above the threshold")
	assert.Equal(t, float64(51), payload["passed_count"])
	assert.Empty(t, payload["raw_output"])
}

func TestTestRun_VerbosityAuto_FullBelowThreshold(t *testing.T) {
	// 5 tests is well below the threshold — auto mode keeps the full
	// payload (passing tests with their elapsed_ms intact).
	queries := &mockQueries{
		testRunFn: func(_ context.Context, _ domain.TestRunRequest) (domain.TestRunResult, error) {
			return domain.TestRunResult{
				Success:    true,
				Tests:      makeTestCases(5, 0),
				Summary:    "5 passed",
				DurationMS: 10,
			}, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "test_run", map[string]any{})
	decoded := decodeTestRunStructured(t, result)
	require.Len(t, decoded.Tests, 5, "auto mode below threshold must keep full per-test list")
	assert.Equal(t, "pass", decoded.Tests[0].Status)
	assert.NotZero(t, decoded.Tests[0].ElapsedMS, "per-test elapsed_ms must survive in full mode")
}

func TestTestRun_VerbositySummary_FailureKeepsContext(t *testing.T) {
	rawStream := `{"Action":"fail","Test":"TestFail0"}` + "\n"
	queries := &mockQueries{
		testRunFn: func(_ context.Context, _ domain.TestRunRequest) (domain.TestRunResult, error) {
			return domain.TestRunResult{
				Success:    false,
				Tests:      makeTestCases(60, 2),
				Summary:    "60 passed, 2 failed",
				RawOutput:  rawStream,
				DurationMS: 999,
			}, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	// Auto mode with 62 tests > threshold picks summary; failures must
	// still appear with file:line + message intact (that is the whole point
	// of summary mode for an agent debugging a red CI run).
	result := callTool(t, cs, "test_run", map[string]any{})
	data, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(data, &payload))

	assert.Equal(t, "summary", payload["verbosity"])
	assert.Equal(t, false, payload["success"])
	assert.Equal(t, float64(60), payload["passed_count"])
	tests, _ := payload["tests"].([]any)
	require.Len(t, tests, 2, "failures must survive in summary mode")
	first, _ := tests[0].(map[string]any)
	assert.Equal(t, "fail", first["status"])
	assert.Equal(t, "pkg/foo_test.go", first["failure_file"])
	assert.Equal(t, "expected x got y", first["failure_message"])
}

func TestTestRun_VerbositySummary_RawOutputKeptWhenIncludeRawOutput(t *testing.T) {
	// Even in summary mode, the include_raw_output escape hatch must work:
	// agents that explicitly want the verbatim go test -json stream should
	// still get it alongside the compact tests slice.
	rawStream := `{"Action":"fail"}` + "\n"
	queries := &mockQueries{
		testRunFn: func(_ context.Context, _ domain.TestRunRequest) (domain.TestRunResult, error) {
			return domain.TestRunResult{
				Success:    false,
				Tests:      makeTestCases(60, 1),
				Summary:    "60 passed, 1 failed",
				RawOutput:  rawStream,
				DurationMS: 100,
			}, nil
		},
	}
	cs := setupTest(t, &mockCommands{}, queries)

	result := callTool(t, cs, "test_run", map[string]any{
		"verbosity":          "summary",
		"include_raw_output": true,
	})
	data, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(data, &payload))

	assert.Equal(t, "summary", payload["verbosity"])
	assert.Equal(t, rawStream, payload["raw_output"])
}
