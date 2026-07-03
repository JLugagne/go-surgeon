package filesystem_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/outbound/filesystem"
	"github.com/stretchr/testify/require"
)

// TestFileSystem_RelativeReadAfterWrite asserts reads and writes resolve a
// relative path to the SAME location when the process cwd differs from the
// captured worktree root (the exact setup the root env var exists for:
// server launched in one directory, root pinned to another). Today writes
// anchor on the root while reads resolve against the cwd, so a write
// followed by a read of the same relative path misses the file — and a
// read-modify-write cycle can write content from one worktree over another.
func TestFileSystem_RelativeReadAfterWrite(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0755))
	elsewhere := t.TempDir()

	t.Setenv("MCP_WORKTREE_ROOT", "")
	t.Setenv("GO_SURGEON_ROOT", root)
	t.Chdir(elsewhere)

	fs := filesystem.NewFileSystem()

	const rel = "gen.go"
	const content = "package gen\n"
	_, err := fs.WriteFile(ctx, rel, []byte(content))
	require.NoError(t, err)

	got, err := fs.ReadFile(ctx, rel)
	require.NoError(t, err, "read must resolve the same relative path the write used")
	require.Equal(t, content, string(got))

	// The write must have landed inside the pinned root, not the cwd.
	_, err = os.Stat(filepath.Join(root, rel))
	require.NoError(t, err, "write must anchor on the captured root")
	_, err = os.Stat(filepath.Join(elsewhere, rel))
	require.True(t, os.IsNotExist(err), "write must not land in the cwd")
}
