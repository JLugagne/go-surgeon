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

// TestNormalizePath_RewritesSymlinkIntoSiblingWorktree verifies that a
// path through a symlink pointing to a sibling worktree is passed
// through unchanged, honoring the caller's intent.
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
	assert.Empty(t, warn)
	assert.Equal(t, target, resolved)
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

// TestFileSystem_WriteFile_HonorsSymlinkWrite verifies
// the end-to-end behavior: a write call with a symlink path pointing
// to a sibling worktree writes through the symlink to the target file.
func TestFileSystem_WriteFile_RewritesCrossWorktreeSymlinkWrite(t *testing.T) {
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

	require.NoError(t, os.WriteFile(filepath.Join(main, "shared", "file.go"), []byte("package p\n"), 0644))

	symlink := filepath.Join(base, "go-mod-link")
	require.NoError(t, os.Symlink(main, symlink))

	t.Setenv(rootEnvVar, worktree)
	fs := NewFileSystem()

	_, err := fs.WriteFile(context.Background(), filepath.Join(symlink, "shared", "file.go"), []byte("package p\n\nvar X = 1\n"))
	require.NoError(t, err, "WriteFile should succeed by writing through the symlink")

	got, err := os.ReadFile(filepath.Join(main, "shared", "file.go"))
	require.NoError(t, err)
	assert.Contains(t, string(got), "var X = 1")

	_, err = os.Stat(filepath.Join(worktree, "shared", "file.go"))
	assert.True(t, os.IsNotExist(err), "worktree file must not be created")
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

// TestFileSystem_MkdirAll_HonorsSymlink verifies the end-to-end
// behavior for directory creation: a symlink path to a sibling
// worktree creates the directory through the symlink.
func TestFileSystem_MkdirAll_HonorsSymlink(t *testing.T) {
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

	mainNew := filepath.Join(main, "shared", "new")
	info, err := os.Stat(mainNew)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	worktreeDir := filepath.Join(canonical(worktree), "shared", "new")
	_, err = os.Stat(worktreeDir)
	assert.True(t, os.IsNotExist(err), "directory must not have been created in worktree")
}

func TestNormalizePath_SiblingWorktreeAbsolutePathIsPassedThrough(t *testing.T) {
	base := t.TempDir()

	worktreeA := filepath.Join(base, "worktree-a")
	worktreeB := filepath.Join(base, "worktree-b")
	require.NoError(t, os.MkdirAll(filepath.Join(worktreeA, "pkg"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(worktreeB, "pkg"), 0755))
	initGitRoot(t, worktreeA)
	initGitRoot(t, worktreeB)

	target := filepath.Join(worktreeB, "pkg", "file.go")

	resolved, _, err := normalizePath(canonical(worktreeA), target)
	require.NoError(t, err)
	assert.NotEqual(t, filepath.Join(worktreeA, "pkg", "file.go"), resolved,
		"must NOT rewrite a plain absolute path in a sibling worktree to root")
	assert.Equal(t, target, resolved,
		"must honor the caller's absolute path")
}
