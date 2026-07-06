package queries_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTestRun_BuildFailureIsNotSuccess asserts a package that fails to
// compile reports Success=false. Before the fix only test-level fail events
// were counted, so a compile error produced \"SUCCESS — 0 passed, 0 failed\"
// and the MCP layer then stripped the raw output containing the only
// evidence.
func TestTestRun_BuildFailureIsNotSuccess(t *testing.T) {
	setupTestModule(t, "package testmod\n\nimport \"testing\"\n\nfunc TestBroken(t *testing.T) {\n\tvar x int = \"not an int\"\n\t_ = x\n}\n")

	h := newHandler(t)
	result, err := h.TestRun(context.Background(), domain.TestRunRequest{})
	require.NoError(t, err)

	assert.False(t, result.Success, "compile failure must not be reported as success (summary=%q)", result.Summary)
	assert.NotEmpty(t, result.RawOutput, "the compile error must stay visible in raw output")
}
