package queries_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/queries"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newBuildCheckHandler returns a real SurgeonQueriesHandler wired against
// a nil filesystem — BuildCheck shells out to `go build` directly and does
// not touch the FS abstraction, so the collaborator can be nil here.
func newBuildCheckHandler() *queries.SurgeonQueriesHandler {
	return queries.NewSurgeonQueriesHandler(nil)
}

// writeGoModule scaffolds a tiny Go module rooted at dir with the given
// files map (relative path → content). A go.mod is written unconditionally.
func writeGoModule(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/buildcheck\n\ngo 1.25\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

// runInDir chdirs into dir for the duration of the test so `go build ./...`
// resolves relative to the temp module. Go's test runner does not parallelize
// by default, so this is safe.
func runInDir(t *testing.T, dir string, fn func()) {
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

func TestBuildCheck_SuccessNoDiagnostics(t *testing.T) {
	dir := t.TempDir()
	writeGoModule(t, dir, map[string]string{
		"pkg/ok.go": "package pkg\n\nfunc Sum(a, b int) int { return a + b }\n",
	})

	runInDir(t, dir, func() {
		h := newBuildCheckHandler()
		res, err := h.BuildCheck(context.Background(), domain.BuildCheckRequest{})
		require.NoError(t, err)
		assert.True(t, res.Success, "expected success, got raw: %s", res.RawOutput)
		assert.Empty(t, res.Diagnostics)
		assert.Equal(t, 0, res.ExitCode)
		assert.False(t, res.TimedOut)
	})
}

func TestBuildCheck_SyntaxErrorReturnsDiagnostic(t *testing.T) {
	dir := t.TempDir()
	writeGoModule(t, dir, map[string]string{
		// Unclosed brace at end of file → parser error with line:col.
		"pkg/broken.go": "package pkg\n\nfunc Broken() {\n",
	})

	runInDir(t, dir, func() {
		h := newBuildCheckHandler()
		res, err := h.BuildCheck(context.Background(), domain.BuildCheckRequest{Dir: "./pkg"})
		require.NoError(t, err)
		assert.False(t, res.Success)
		require.NotEmpty(t, res.Diagnostics, "expected at least one diagnostic; raw=%s", res.RawOutput)
		d := res.Diagnostics[0]
		assert.Contains(t, d.File, "broken.go")
		assert.Greater(t, d.Line, 0)
		assert.NotEmpty(t, d.Message)
	})
}

func TestBuildCheck_TypeErrorReturnsDiagnostic(t *testing.T) {
	dir := t.TempDir()
	writeGoModule(t, dir, map[string]string{
		"pkg/typeerr.go": "package pkg\n\nfunc Bad() {\n\tvar x int = \"not an int\"\n\t_ = x\n}\n",
	})

	runInDir(t, dir, func() {
		h := newBuildCheckHandler()
		res, err := h.BuildCheck(context.Background(), domain.BuildCheckRequest{Dir: "./pkg"})
		require.NoError(t, err)
		assert.False(t, res.Success)
		require.NotEmpty(t, res.Diagnostics, "expected at least one diagnostic; raw=%s", res.RawOutput)

		found := false
		for _, d := range res.Diagnostics {
			if strings.Contains(d.File, "typeerr.go") && d.Line > 0 && d.Column > 0 {
				found = true
				break
			}
		}
		assert.True(t, found, "expected a typeerr.go diagnostic with line/col; got %+v", res.Diagnostics)
	})
}

func TestBuildCheck_RejectsAbsolutePath(t *testing.T) {
	h := newBuildCheckHandler()
	_, err := h.BuildCheck(context.Background(), domain.BuildCheckRequest{Dir: "/etc"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute")
}

func TestBuildCheck_RejectsParentTraversal(t *testing.T) {
	h := newBuildCheckHandler()
	_, err := h.BuildCheck(context.Background(), domain.BuildCheckRequest{Dir: "../escape"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "within the project root")
}

func TestBuildCheck_TimeoutIsReported(t *testing.T) {
	dir := t.TempDir()
	// An empty package still goes through the compiler; setting timeout to
	// 1ns guarantees the context fires before `go build` can even start.
	writeGoModule(t, dir, map[string]string{
		"pkg/ok.go": "package pkg\n\nfunc Trivial() int { return 1 }\n",
	})

	runInDir(t, dir, func() {
		h := newBuildCheckHandler()
		ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
		defer cancel()
		res, err := h.BuildCheck(ctx, domain.BuildCheckRequest{Dir: "./pkg"})
		// TimedOut can come either via exec killed by deadline (runErr ExitError)
		// or via the user ctx cancellation. We accept either path, but the
		// caller-visible fields should reflect the failure.
		if err != nil {
			// exec.LookPath succeeded; ctx cancellation propagated as an error.
			assert.Contains(t, err.Error(), "failed to run go build")
			return
		}
		assert.False(t, res.Success)
		assert.True(t, res.TimedOut || res.ExitCode != 0, "expected timeout or non-zero exit; got %+v", res)
	})
}

func TestBuildCheck_AffectedByLeafPackageBuildsOnlyOwner(t *testing.T) {
	dir := t.TempDir()
	// "leaf" imports nothing from the module and is imported by nothing.
	// rev-dep closure is just leaf itself.
	writeGoModule(t, dir, map[string]string{
		"leaf/leaf.go":     "package leaf\n\nfunc Zero() int { return 0 }\n",
		"other/other.go":   "package other\n\nfunc One() int { return 1 }\n",
		"consumer/use.go":  "package consumer\n\nimport \"example.com/buildcheck/other\"\n\nfunc Use() int { return other.One() }\n",
	})

	runInDir(t, dir, func() {
		h := newBuildCheckHandler()
		target := filepath.Join(dir, "leaf", "leaf.go")
		res, err := h.BuildCheck(context.Background(), domain.BuildCheckRequest{AffectedBy: target})
		require.NoError(t, err)
		assert.True(t, res.Success, "raw=%s", res.RawOutput)
		require.Len(t, res.Packages, 1, "leaf has no reverse-deps; only owner should be built: %v", res.Packages)
		assert.Contains(t, res.Packages[0], "leaf")
	})
}

func TestBuildCheck_AffectedByIncludesReverseDeps(t *testing.T) {
	dir := t.TempDir()
	// shared <- midA, midB; midA <- top. Editing shared must rebuild all four.
	writeGoModule(t, dir, map[string]string{
		"shared/s.go": "package shared\n\nfunc V() int { return 42 }\n",
		"midA/a.go":   "package midA\n\nimport \"example.com/buildcheck/shared\"\n\nfunc A() int { return shared.V() }\n",
		"midB/b.go":   "package midB\n\nimport \"example.com/buildcheck/shared\"\n\nfunc B() int { return shared.V() }\n",
		"top/t.go":    "package top\n\nimport \"example.com/buildcheck/midA\"\n\nfunc T() int { return midA.A() }\n",
		"unrelated/u.go": "package unrelated\n\nfunc U() int { return 7 }\n",
	})

	runInDir(t, dir, func() {
		h := newBuildCheckHandler()
		target := filepath.Join(dir, "shared", "s.go")
		res, err := h.BuildCheck(context.Background(), domain.BuildCheckRequest{AffectedBy: target})
		require.NoError(t, err)
		assert.True(t, res.Success, "raw=%s", res.RawOutput)

		joined := strings.Join(res.Packages, " ")
		assert.Contains(t, joined, "shared", "owner missing from %v", res.Packages)
		assert.Contains(t, joined, "midA", "direct rdep missing from %v", res.Packages)
		assert.Contains(t, joined, "midB", "direct rdep missing from %v", res.Packages)
		assert.Contains(t, joined, "top", "transitive rdep missing from %v", res.Packages)
		assert.NotContains(t, joined, "unrelated", "unrelated pkg should not be built: %v", res.Packages)
	})
}

func TestBuildCheck_AffectedByRejectsDirCombo(t *testing.T) {
	dir := t.TempDir()
	writeGoModule(t, dir, map[string]string{
		"pkg/ok.go": "package pkg\n\nfunc F() int { return 1 }\n",
	})
	runInDir(t, dir, func() {
		h := newBuildCheckHandler()
		target := filepath.Join(dir, "pkg", "ok.go")
		_, err := h.BuildCheck(context.Background(), domain.BuildCheckRequest{Dir: "./pkg", AffectedBy: target})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mutually exclusive")
	})
}

func TestBuildCheck_AffectedByFileOutsideModule(t *testing.T) {
	dir := t.TempDir()
	writeGoModule(t, dir, map[string]string{
		"pkg/ok.go": "package pkg\n\nfunc F() int { return 1 }\n",
	})
	// Write a file outside the module tree.
	outside := filepath.Join(t.TempDir(), "stray.go")
	require.NoError(t, os.WriteFile(outside, []byte("package stray\n"), 0644))

	runInDir(t, dir, func() {
		h := newBuildCheckHandler()
		_, err := h.BuildCheck(context.Background(), domain.BuildCheckRequest{AffectedBy: outside})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not inside any package")
	})
}

func TestBuildCheck_DeduplicatesDiagnostics(t *testing.T) {
	dir := t.TempDir()
	// Two files referring to the same missing symbol — go build emits one
	// diagnostic per reference site. Dedup logic should still return the
	// set of unique (file,line,col,msg) tuples.
	writeGoModule(t, dir, map[string]string{
		"pkg/a.go": "package pkg\n\nfunc A() { _ = missingSymbol }\n",
		"pkg/b.go": "package pkg\n\nfunc B() { _ = missingSymbol }\n",
	})

	runInDir(t, dir, func() {
		h := newBuildCheckHandler()
		res, err := h.BuildCheck(context.Background(), domain.BuildCheckRequest{Dir: "./pkg"})
		require.NoError(t, err)
		assert.False(t, res.Success)
		require.NotEmpty(t, res.Diagnostics)

		seen := map[string]int{}
		for _, d := range res.Diagnostics {
			key := d.File + "|" + strings.TrimSpace(d.Message)
			seen[key]++
		}
		for k, n := range seen {
			assert.LessOrEqual(t, n, 1, "diagnostic %s appeared %d times", k, n)
		}
	})
}
