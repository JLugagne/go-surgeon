package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chdirT chdirs into dir for the duration of the test, restoring the
// original cwd afterwards.
func chdirT(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() {
		_ = os.Chdir(prev)
	})
}

// initGitRoot makes dir look like a git worktree root by creating a
// .git directory (so findGitWorktreeRoot stops there).
func initGitRoot(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0755))
}

func TestNormalizePath_AllowsSameWorktree(t *testing.T) {
	root := t.TempDir()
	initGitRoot(t, root)

	resolved, warn, err := normalizePath(canonical(root), filepath.Join(root, "pkg", "file.go"))
	require.NoError(t, err)
	assert.Empty(t, warn)
	assert.Equal(t, filepath.Join(root, "pkg", "file.go"), resolved)
}

// TestNormalizePath_RewritesSymlinkIntoSiblingWorktree is the headline
// behavior change: a path that resolves through a symlink into a
// sibling worktree no longer errors — it is rewritten to land inside
// our root, with a warning describing the rewrite.
func TestNormalizePath_RewritesSymlinkIntoSiblingWorktree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	base := t.TempDir()

	main := filepath.Join(base, "main")
	worktree := filepath.Join(base, "worktree")
	require.NoError(t, os.MkdirAll(filepath.Join(main, "pkg"), 0755))
	require.NoError(t, os.MkdirAll(worktree, 0755))
	initGitRoot(t, main)
	initGitRoot(t, worktree)

	// External symlink mimicking /go/<module> -> main, the path the
	// Claude Code harness presents to agents.
	symlink := filepath.Join(base, "go-mod-link")
	require.NoError(t, os.Symlink(main, symlink))

	target := filepath.Join(symlink, "pkg", "file.go")
	resolved, warn, err := normalizePath(canonical(worktree), target)
	require.NoError(t, err)
	assert.NotEmpty(t, warn, "expected a warning describing the rewrite")
	assert.Equal(t, filepath.Join(canonical(worktree), "pkg", "file.go"), resolved)
	assert.Contains(t, warn, "rewrote")
}

func TestNormalizePath_EmptyRootIsBestEffort(t *testing.T) {
	resolved, warn, err := normalizePath("", "/tmp/somewhere/file.go")
	require.NoError(t, err)
	assert.Empty(t, warn)
	assert.Equal(t, "/tmp/somewhere/file.go", resolved)
}

func TestNormalizePath_AllowsGOMODCACHE(t *testing.T) {
	base := t.TempDir()
	initGitRoot(t, base)

	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)
	target := filepath.Join(cache, "github.com", "x", "y@v1.0.0", "file.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0755))

	resolved, warn, err := normalizePath(canonical(base), target)
	require.NoError(t, err)
	assert.Empty(t, warn)
	assert.Equal(t, target, resolved)
}

func TestNormalizePath_RelativePathAnchorsOnRoot(t *testing.T) {
	tmp := t.TempDir()
	initGitRoot(t, tmp)

	resolved, warn, err := normalizePath(canonical(tmp), "internal/foo/bar.go")
	require.NoError(t, err)
	assert.Empty(t, warn)
	assert.Equal(t, filepath.Join(canonical(tmp), "internal/foo/bar.go"), resolved)
}

// TestNormalizePath_OutsideAnyWorktreeIsAllowed: a path that lives
// outside any git worktree (a /tmp fixture, an unrelated subtree) is
// passed through unchanged. The cross-worktree symlink failure mode
// cannot apply there, and refusing would break callers that legitimately
// write to t.TempDir() while the server's captured root is the repo.
func TestNormalizePath_OutsideAnyWorktreeIsAllowed(t *testing.T) {
	tmp := t.TempDir()
	worktree := filepath.Join(tmp, "worktree")
	stray := filepath.Join(tmp, "stray", "x.go")
	initGitRoot(t, worktree)
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "stray"), 0755))

	resolved, warn, err := normalizePath(canonical(worktree), stray)
	require.NoError(t, err)
	assert.Empty(t, warn)
	assert.Equal(t, stray, resolved)
}

func TestResolveRoot_HonorsEnvVar(t *testing.T) {
	tmp := t.TempDir()
	initGitRoot(t, tmp)
	t.Setenv(rootEnvVar, tmp)

	got := resolveRoot()
	assert.Equal(t, canonical(tmp), got)
}

func TestNewFileSystem_CapturesRootAtConstruction(t *testing.T) {
	tmp := t.TempDir()
	initGitRoot(t, tmp)
	t.Setenv(rootEnvVar, tmp)

	fs := NewFileSystem()
	assert.Equal(t, canonical(tmp), fs.root)
}

// TestFileSystem_WriteFile_RewritesCrossWorktreeSymlinkWrite verifies
// the end-to-end behavior change: a write call with a sibling-worktree
// path actually creates the file inside our root, never in the parent
// checkout.
func TestFileSystem_WriteFile_RewritesCrossWorktreeSymlinkWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	base := t.TempDir()

	main := filepath.Join(base, "main")
	worktree := filepath.Join(base, "worktree")
	require.NoError(t, os.MkdirAll(filepath.Join(main, "shared"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(worktree, "shared"), 0755))
	initGitRoot(t, main)
	initGitRoot(t, worktree)

	require.NoError(t, os.WriteFile(filepath.Join(main, "shared", "file.go"), []byte("package p\n"), 0644))

	symlink := filepath.Join(base, "go-mod-link")
	require.NoError(t, os.Symlink(main, symlink))

	t.Setenv(rootEnvVar, worktree)
	fs := NewFileSystem()

	_, err := fs.WriteFile(context.Background(), filepath.Join(symlink, "shared", "file.go"), []byte("package p\n\nvar X = 1\n"))
	require.NoError(t, err, "WriteFile should succeed by rewriting into worktree, not error")

	rewrittenPath := filepath.Join(canonical(worktree), "shared", "file.go")
	got, err := os.ReadFile(rewrittenPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "var X = 1")

	mainContent, err := os.ReadFile(filepath.Join(main, "shared", "file.go"))
	require.NoError(t, err)
	assert.Equal(t, "package p\n", string(mainContent), "parent checkout file must remain untouched")
}

func TestFileSystem_WriteFile_AllowsInCapturedWorktree(t *testing.T) {
	worktree := t.TempDir()
	initGitRoot(t, worktree)
	t.Setenv(rootEnvVar, worktree)

	fs := NewFileSystem()
	target := filepath.Join(worktree, "file.go")
	_, err := fs.WriteFile(context.Background(), target, []byte("package p\n"))
	require.NoError(t, err)
	content, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(content), "package p"))
}

// TestFileSystem_MkdirAll_RewritesCrossWorktree mirrors the WriteFile
// behavior for directory creation: a sibling-worktree path is rewritten
// into our root rather than refused.
func TestFileSystem_MkdirAll_RewritesCrossWorktree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	base := t.TempDir()

	main := filepath.Join(base, "main")
	worktree := filepath.Join(base, "worktree")
	require.NoError(t, os.MkdirAll(filepath.Join(main, "shared"), 0755))
	require.NoError(t, os.MkdirAll(worktree, 0755))
	initGitRoot(t, main)
	initGitRoot(t, worktree)

	symlink := filepath.Join(base, "go-mod-link")
	require.NoError(t, os.Symlink(main, symlink))

	t.Setenv(rootEnvVar, worktree)
	fs := NewFileSystem()

	require.NoError(t, fs.MkdirAll(context.Background(), filepath.Join(symlink, "shared", "new")))

	worktreeDir := filepath.Join(canonical(worktree), "shared", "new")
	info, err := os.Stat(worktreeDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	mainNew := filepath.Join(main, "shared", "new")
	_, err = os.Stat(mainNew)
	assert.True(t, os.IsNotExist(err), "directory must not have been created in parent checkout")
}
