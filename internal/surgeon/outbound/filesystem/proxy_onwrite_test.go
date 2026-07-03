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

// TestProxy_OnWriteFires asserts the OnWrite hook fires on writes and
// deletes — Setup relies on it to invalidate the shared loader cache after
// go-surgeon's own edits.
func TestProxy_OnWriteFires(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	t.Setenv("MCP_WORKTREE_ROOT", "")
	t.Setenv("GO_SURGEON_ROOT", tmpDir)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755))

	proxy := &filesystem.ProxyFileSystem{Active: filesystem.NewDryRunFileSystem(filesystem.NewFileSystem())}
	fired := 0
	proxy.OnWrite = func() { fired++ }

	target := filepath.Join(tmpDir, "a.go")
	_, err := proxy.WriteFile(ctx, target, []byte("package a\n"))
	require.NoError(t, err)
	require.NoError(t, proxy.DeleteFile(ctx, target))
	assert.Equal(t, 2, fired, "both write and delete must fire the hook")
}
