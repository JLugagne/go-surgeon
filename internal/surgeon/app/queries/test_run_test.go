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

// writeTestModule scaffolds a tiny Go module rooted at dir with the given
// files map (relative path -> content). A go.mod is written unconditionally.
// Mirrors the helper in build_check_test.go but uses a distinct module name
// so cache keys don't collide across tests.
func writeTestModule(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/testrun\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

// runInTestDir chdirs into dir for the duration of the test so `go test ./...`
// resolves relative to the temp module.
func runInTestDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	fn()
}

func TestTestRun_AffectedByLeafPackageRunsOnlyOwner(t *testing.T) {
	dir := t.TempDir()
	// "leaf" is imported by nothing, so its rev-dep closure is just itself.
	// "other" has its own tests that must NOT run when affected_by targets
	// leaf.
	writeTestModule(t, dir, map[string]string{
		"leaf/leaf.go": "package leaf\n\nfunc Zero() int { return 0 }\n",
		"leaf/leaf_test.go": "package leaf\n\nimport \"testing\"\n\n" +
			"func TestLeafZero(t *testing.T) { if Zero() != 0 { t.Fatal(\"bad\") } }\n",
		"other/other.go": "package other\n\nfunc One() int { return 1 }\n",
		"other/other_test.go": "package other\n\nimport \"testing\"\n\n" +
			"func TestOtherOne(t *testing.T) { if One() != 1 { t.Fatal(\"bad\") } }\n",
	})

	runInTestDir(t, dir, func() {
		h := newHandler(t)
		target := filepath.Join(dir, "leaf", "leaf.go")
		result, err := h.TestRun(context.Background(), domain.TestRunRequest{AffectedBy: target})
		require.NoError(t, err)
		assert.True(t, result.Success, "raw=%s", result.RawOutput)

		// Only leaf's package should appear in Packages.
		require.Len(t, result.Packages, 1, "expected only owner package: got %v", result.Packages)
		assert.Contains(t, result.Packages[0], "leaf")

		// TestLeafZero must have run; TestOtherOne must NOT have run.
		var ranLeaf, ranOther bool
		for _, tc := range result.Tests {
			if tc.Name == "TestLeafZero" {
				ranLeaf = true
			}
			if tc.Name == "TestOtherOne" {
				ranOther = true
			}
		}
		assert.True(t, ranLeaf, "TestLeafZero should have run; tests=%+v", result.Tests)
		assert.False(t, ranOther, "TestOtherOne must not run when affected_by targets leaf; tests=%+v", result.Tests)
	})
}

func TestTestRun_AffectedByIncludesReverseDeps(t *testing.T) {
	dir := t.TempDir()
	// shared <- midA, midB; midA <- top. Editing shared must retest all four.
	// "unrelated" has its own tests that must NOT run.
	writeTestModule(t, dir, map[string]string{
		"shared/s.go": "package shared\n\nfunc V() int { return 42 }\n",
		"shared/s_test.go": "package shared\n\nimport \"testing\"\n\n" +
			"func TestSharedV(t *testing.T) { if V() != 42 { t.Fatal(\"bad\") } }\n",
		"midA/a.go": "package midA\n\nimport \"example.com/testrun/shared\"\n\n" +
			"func A() int { return shared.V() }\n",
		"midA/a_test.go": "package midA\n\nimport \"testing\"\n\n" +
			"func TestMidA(t *testing.T) { if A() != 42 { t.Fatal(\"bad\") } }\n",
		"midB/b.go": "package midB\n\nimport \"example.com/testrun/shared\"\n\n" +
			"func B() int { return shared.V() }\n",
		"midB/b_test.go": "package midB\n\nimport \"testing\"\n\n" +
			"func TestMidB(t *testing.T) { if B() != 42 { t.Fatal(\"bad\") } }\n",
		"top/t.go": "package top\n\nimport \"example.com/testrun/midA\"\n\n" +
			"func T() int { return midA.A() }\n",
		"top/t_test.go": "package top\n\nimport \"testing\"\n\n" +
			"func TestTop(t *testing.T) { if T() != 42 { t.Fatal(\"bad\") } }\n",
		"unrelated/u.go": "package unrelated\n\nfunc U() int { return 7 }\n",
		"unrelated/u_test.go": "package unrelated\n\nimport \"testing\"\n\n" +
			"func TestUnrelated(t *testing.T) { if U() != 7 { t.Fatal(\"bad\") } }\n",
	})

	runInTestDir(t, dir, func() {
		h := newHandler(t)
		target := filepath.Join(dir, "shared", "s.go")
		result, err := h.TestRun(context.Background(), domain.TestRunRequest{AffectedBy: target})
		require.NoError(t, err)
		assert.True(t, result.Success, "raw=%s", result.RawOutput)

		joined := strings.Join(result.Packages, " ")
		assert.Contains(t, joined, "shared", "owner missing from %v", result.Packages)
		assert.Contains(t, joined, "midA", "direct rdep missing from %v", result.Packages)
		assert.Contains(t, joined, "midB", "direct rdep missing from %v", result.Packages)
		assert.Contains(t, joined, "top", "transitive rdep missing from %v", result.Packages)
		assert.NotContains(t, joined, "unrelated", "unrelated pkg should not be tested: %v", result.Packages)

		var ranUnrelated bool
		for _, tc := range result.Tests {
			if tc.Name == "TestUnrelated" {
				ranUnrelated = true
			}
		}
		assert.False(t, ranUnrelated, "TestUnrelated must not run; tests=%+v", result.Tests)
	})
}

func TestTestRun_AffectedByRejectsDirCombo(t *testing.T) {
	dir := t.TempDir()
	writeTestModule(t, dir, map[string]string{
		"pkg/ok.go": "package pkg\n\nfunc F() int { return 1 }\n",
	})
	runInTestDir(t, dir, func() {
		h := newHandler(t)
		target := filepath.Join(dir, "pkg", "ok.go")
		_, err := h.TestRun(context.Background(), domain.TestRunRequest{Dir: "./pkg", AffectedBy: target})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mutually exclusive")
	})
}
