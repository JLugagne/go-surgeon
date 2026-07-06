package filesystem_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/outbound/filesystem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDryRun_DeleteFile_AppearsInDiffs asserts a dry-run delete is visible:
// CollectDiffs must render the removal and ReadFile must report the file as
// gone, while the real file stays on disk. Today deletions are recorded but
// never diffed, so `--dry-run` on a delete prints nothing at all.
func TestDryRun_DeleteFile_AppearsInDiffs(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	t.Setenv("MCP_WORKTREE_ROOT", "")
	t.Setenv("GO_SURGEON_ROOT", tmpDir)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755))
	realFS := filesystem.NewFileSystem()
	dryFS := filesystem.NewDryRunFileSystem(realFS)

	testFile := filepath.Join(tmpDir, "doomed.go")
	require.NoError(t, os.WriteFile(testFile, []byte("package main\n"), 0644))

	require.NoError(t, dryFS.DeleteFile(ctx, testFile))

	diffs, err := dryFS.CollectDiffs(ctx)
	require.NoError(t, err)
	assert.Contains(t, diffs, "doomed.go", "deletion must appear in the dry-run diff")
	assert.Contains(t, diffs, "-package main", "removed lines must appear in the dry-run diff")

	_, err = dryFS.ReadFile(ctx, testFile)
	assert.True(t, os.IsNotExist(err), "reading a dry-run-deleted file must report not-exist, got: %v", err)

	_, err = os.Stat(testFile)
	assert.NoError(t, err, "dry run must not touch the real file")
}

// TestDryRun_WriteAfterDelete_Recreates asserts a write after a dry-run
// delete brings the file back (mirrors delete-then-recreate plans).
func TestDryRun_WriteAfterDelete_Recreates(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	t.Setenv("MCP_WORKTREE_ROOT", "")
	t.Setenv("GO_SURGEON_ROOT", tmpDir)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755))
	realFS := filesystem.NewFileSystem()
	dryFS := filesystem.NewDryRunFileSystem(realFS)

	testFile := filepath.Join(tmpDir, "phoenix.go")
	require.NoError(t, os.WriteFile(testFile, []byte("package old\n"), 0644))

	require.NoError(t, dryFS.DeleteFile(ctx, testFile))
	_, err := dryFS.WriteFile(ctx, testFile, []byte("package reborn\n"))
	require.NoError(t, err)

	content, err := dryFS.ReadFile(ctx, testFile)
	require.NoError(t, err, "a rewritten file must be readable again")
	assert.Contains(t, string(content), "reborn")
}
