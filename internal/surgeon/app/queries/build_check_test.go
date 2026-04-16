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
