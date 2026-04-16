package queries_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/queries"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/outbound/filesystem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestModule writes a tiny Go module in a tempdir and chdirs there so
// that `go test ./...` resolves local packages. Returns the tempdir and
// registers cleanup.
func setupTestModule(t *testing.T, goFile string) string {
	t.Helper()
	dir := t.TempDir()

	goMod := "module testmod\n\ngo 1.21\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "math_test.go"), []byte(goFile), 0o644))

	origWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	return dir
}

func newHandler(t *testing.T) *queries.SurgeonQueriesHandler {
	t.Helper()
	return queries.NewSurgeonQueriesHandler(filesystem.NewFileSystem())
}

func TestTestRun_PassingTests(t *testing.T) {
	setupTestModule(t, `package testmod

import "testing"

func TestAdd(t *testing.T) {
	if 1+1 != 2 {
		t.Fatal("math is broken")
	}
}

func TestSub(t *testing.T) {
	if 2-1 != 1 {
		t.Fatal("subtraction failed")
	}
}
`)

	h := newHandler(t)
	result, err := h.TestRun(context.Background(), domain.TestRunRequest{})
	require.NoError(t, err)

	assert.True(t, result.Success, "expected success, got: %+v", result)
	assert.Len(t, result.Tests, 2)
	for _, tc := range result.Tests {
		assert.Equal(t, "pass", tc.Status, "test %s should pass", tc.Name)
		assert.Empty(t, tc.OutputLines, "passing tests should have empty output")
	}
	assert.Contains(t, result.Summary, "2 passed")
	assert.Contains(t, result.Summary, "0 failed")
	assert.False(t, result.TimedOut)
}

func TestTestRun_FailingTestCapturesFileLine(t *testing.T) {
	setupTestModule(t, `package testmod

import "testing"

func TestBadMath(t *testing.T) {
	t.Errorf("unexpected result: %d", 42)
}
`)

	h := newHandler(t)
	result, err := h.TestRun(context.Background(), domain.TestRunRequest{})
	require.NoError(t, err)

	assert.False(t, result.Success)
	require.Len(t, result.Tests, 1)
	tc := result.Tests[0]
	assert.Equal(t, "fail", tc.Status)
	assert.Equal(t, "TestBadMath", tc.Name)
	assert.Contains(t, tc.FailureFile, "math_test.go")
	assert.Equal(t, 6, tc.FailureLine, "t.Errorf is on line 6")
	assert.Contains(t, tc.FailureMessage, "unexpected result: 42")
	assert.NotEmpty(t, tc.OutputLines)
	assert.Contains(t, result.Summary, "0 passed")
	assert.Contains(t, result.Summary, "1 failed")
}

func TestTestRun_RunFilterSelectsSubset(t *testing.T) {
	setupTestModule(t, `package testmod

import "testing"

func TestAlpha(t *testing.T) {}
func TestBeta(t *testing.T)  {}
func TestGamma(t *testing.T) {}
`)

	h := newHandler(t)
	result, err := h.TestRun(context.Background(), domain.TestRunRequest{Run: "TestBeta"})
	require.NoError(t, err)

	assert.True(t, result.Success)
	require.Len(t, result.Tests, 1, "expected only TestBeta to run")
	assert.Equal(t, "TestBeta", result.Tests[0].Name)
}

func TestTestRun_TimeoutMarksResult(t *testing.T) {
	setupTestModule(t, `package testmod

import (
	"testing"
	"time"
)

func TestSlow(t *testing.T) {
	time.Sleep(10 * time.Second)
}
`)

	h := newHandler(t)
	result, err := h.TestRun(context.Background(), domain.TestRunRequest{TimeoutSeconds: 2})
	require.NoError(t, err)

	assert.False(t, result.Success)
	assert.True(t, result.TimedOut, "expected timed_out=true, got: %+v", result)
}

func TestTestRun_RejectsAbsoluteDir(t *testing.T) {
	h := newHandler(t)
	_, err := h.TestRun(context.Background(), domain.TestRunRequest{Dir: "/etc"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "relative")
}

func TestTestRun_RejectsEscapingDir(t *testing.T) {
	h := newHandler(t)
	_, err := h.TestRun(context.Background(), domain.TestRunRequest{Dir: "../../../etc"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes")
}

func TestTestRun_RejectsSuspiciousTags(t *testing.T) {
	h := newHandler(t)
	_, err := h.TestRun(context.Background(), domain.TestRunRequest{Tags: "foo; rm -rf /"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid build tags")
}

func TestTestRun_ClampsTimeout(t *testing.T) {
	// We can't actually wait 600s; just verify that the target string
	// resolution accepts the large value without error paths firing.
	// Use a trivial passing test and pass an over-limit value.
	setupTestModule(t, `package testmod

import "testing"

func TestTrivial(t *testing.T) {}
`)
	h := newHandler(t)
	// Use TimeoutSeconds well above the cap; clamping is internal, we
	// just ensure the call still succeeds and doesn't reject.
	result, err := h.TestRun(context.Background(), domain.TestRunRequest{TimeoutSeconds: 99999})
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestTestRun_SummaryPluralPackages(t *testing.T) {
	// Sanity check that summary phrasing works when zero tests ran (empty
	// module). This ensures we don't crash on the edge case.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module empty\n\ngo 1.21\n"), 0o644))
	origWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	h := newHandler(t)
	result, err := h.TestRun(context.Background(), domain.TestRunRequest{})
	require.NoError(t, err)
	assert.True(t, strings.Contains(result.Summary, "passed"))
}
