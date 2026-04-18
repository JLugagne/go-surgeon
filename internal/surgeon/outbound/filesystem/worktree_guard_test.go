package filesystem

import (
	"context"
	"os"
	"path/filepath"
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

func TestGuardWriteInWorktree_AllowsSameWorktree(t *testing.T) {
	root := t.TempDir()
	initGitRoot(t, root)
	chdirT(t, root)

	err := guardWriteInWorktree(filepath.Join(root, "pkg", "file.go"))
	assert.NoError(t, err)
}

func TestGuardWriteInWorktree_RefusesSymlinkIntoSiblingWorktree(t *testing.T) {
	base := t.TempDir()

	main := filepath.Join(base, "main")
	worktree := filepath.Join(base, "worktree")
	require.NoError(t, os.MkdirAll(main, 0755))
	require.NoError(t, os.MkdirAll(worktree, 0755))
	initGitRoot(t, main)
	initGitRoot(t, worktree)

	// Inside the worktree, a sub-path "shared" is symlinked to main's
	// "shared" directory — mirrors the /go/<module> symlink in the
	// real environment.
	realShared := filepath.Join(main, "shared")
	require.NoError(t, os.MkdirAll(realShared, 0755))
	linkShared := filepath.Join(worktree, "shared")
	require.NoError(t, os.Symlink(realShared, linkShared))

	chdirT(t, worktree)

	// Write target follows the symlink into main — must be refused.
	target := filepath.Join(linkShared, "file.go")
	err := guardWriteInWorktree(target)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing write")
	assert.Contains(t, err.Error(), "outside the current worktree")
}

func TestGuardWriteInWorktree_AllowsWhenCwdNotInWorktree(t *testing.T) {
	base := t.TempDir() // no .git → not a worktree
	chdirT(t, base)

	// Target happens to be inside a worktree elsewhere; that's fine
	// because the caller isn't in one, so we can't reason about intent.
	err := guardWriteInWorktree(filepath.Join(base, "file.go"))
	assert.NoError(t, err)
}

func TestGuardWriteInWorktree_AllowsGOMODCACHE(t *testing.T) {
	base := t.TempDir()
	initGitRoot(t, base)
	chdirT(t, base)

	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)
	target := filepath.Join(cache, "github.com", "x", "y@v1.0.0", "file.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0755))

	err := guardWriteInWorktree(target)
	assert.NoError(t, err)
}

func TestFileSystem_WriteFile_RefusesCrossWorktreeSymlinkWrite(t *testing.T) {
	base := t.TempDir()

	main := filepath.Join(base, "main")
	worktree := filepath.Join(base, "worktree")
	require.NoError(t, os.MkdirAll(main, 0755))
	require.NoError(t, os.MkdirAll(worktree, 0755))
	initGitRoot(t, main)
	initGitRoot(t, worktree)

	realShared := filepath.Join(main, "shared")
	require.NoError(t, os.MkdirAll(realShared, 0755))
	// Seed a real file in main so we can prove it's not rewritten.
	require.NoError(t, os.WriteFile(filepath.Join(realShared, "file.go"), []byte("package p\n"), 0644))

	linkShared := filepath.Join(worktree, "shared")
	require.NoError(t, os.Symlink(realShared, linkShared))

	chdirT(t, worktree)

	fs := NewFileSystem()
	_, err := fs.WriteFile(context.Background(), filepath.Join(linkShared, "file.go"), []byte("package p\n\nvar X = 1\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside the current worktree")

	// Confirm main's original file was not overwritten.
	got, err := os.ReadFile(filepath.Join(realShared, "file.go"))
	require.NoError(t, err)
	assert.Equal(t, "package p\n", string(got))
}

func TestFileSystem_WriteFile_AllowsInCwdWorktree(t *testing.T) {
	worktree := t.TempDir()
	initGitRoot(t, worktree)
	chdirT(t, worktree)

	fs := NewFileSystem()
	target := filepath.Join(worktree, "file.go")
	_, err := fs.WriteFile(context.Background(), target, []byte("package p\n"))
	require.NoError(t, err)
	content, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(content), "package p"))
}

func TestFileSystem_MkdirAll_RefusesCrossWorktree(t *testing.T) {
	base := t.TempDir()

	main := filepath.Join(base, "main")
	worktree := filepath.Join(base, "worktree")
	require.NoError(t, os.MkdirAll(main, 0755))
	require.NoError(t, os.MkdirAll(worktree, 0755))
	initGitRoot(t, main)
	initGitRoot(t, worktree)

	realShared := filepath.Join(main, "shared")
	require.NoError(t, os.MkdirAll(realShared, 0755))
	linkShared := filepath.Join(worktree, "shared")
	require.NoError(t, os.Symlink(realShared, linkShared))

	chdirT(t, worktree)

	fs := NewFileSystem()
	err := fs.MkdirAll(context.Background(), filepath.Join(linkShared, "new"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside the current worktree")
}
