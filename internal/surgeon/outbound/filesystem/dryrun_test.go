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

func TestDryRunFileSystem(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	// Anchor FileSystem on the tmpdir so writes under it don't trip
	// the cross-worktree guard that resolves cwd to the repo root.
	t.Setenv("GO_SURGEON_ROOT", tmpDir)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755))
	realFS := filesystem.NewFileSystem()
	dryFS := filesystem.NewDryRunFileSystem(realFS)

	testFile := filepath.Join(tmpDir, "test.go")

	err := os.WriteFile(testFile, []byte("package main\n"), 0644)
	require.NoError(t, err)

	_, err = dryFS.WriteFile(ctx, testFile, []byte("package main\n\nfunc main() {}\n"))
	require.NoError(t, err)

	content, err := dryFS.ReadFile(ctx, testFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), "func main()")

	realContent, err := realFS.ReadFile(ctx, testFile)
	require.NoError(t, err)
	assert.NotContains(t, string(realContent), "func main()")
}

func TestProxyFileSystem(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	t.Setenv("GO_SURGEON_ROOT", tmpDir)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755))
	realFS := filesystem.NewFileSystem()
	proxy := &filesystem.ProxyFileSystem{Active: realFS}

	testFile := filepath.Join(tmpDir, "proxy.txt")

	_, err := proxy.WriteFile(ctx, testFile, []byte("hello"))
	require.NoError(t, err)

	content, err := proxy.ReadFile(ctx, testFile)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content))
}
