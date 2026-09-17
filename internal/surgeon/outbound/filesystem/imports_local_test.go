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

// TestWriteFile_ResolvesLocalModuleImport reproduces issue #34: after an
// edit, import resolution must prefer a package of the current module
// (including its internal/ tree) over any same-named third-party package
// and must not leave the qualifier unresolved.
func TestWriteFile_ResolvesLocalModuleImport(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("GO_SURGEON_ROOT", tmpDir)

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "go.mod"),
		[]byte("module example.com/localmod\n\ngo 1.26\n"), 0644))

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "internal", "store"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "internal", "store", "store.go"),
		[]byte("package store\n\nfunc Get() string { return \"\" }\n"), 0644))

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "internal", "app"), 0755))

	fs := filesystem.NewFileSystem()
	src := []byte("package app\n\nfunc F() string { return store.Get() }\n")

	added, err := fs.WriteFile(context.Background(), "internal/app/app.go", src)
	require.NoError(t, err)

	written, err := os.ReadFile(filepath.Join(tmpDir, "internal", "app", "app.go"))
	require.NoError(t, err)
	assert.NotContains(t, string(written), "go.etcd.io")
	assert.Contains(t, string(written), "example.com/localmod/internal/store")
	assert.Contains(t, added, "example.com/localmod/internal/store")
}

// TestWriteFile_ResolvesMultipleLocalImports covers the "added no import
// at all" half of issue #34: every local qualifier must be imported in a
// single write.
func TestWriteFile_ResolvesMultipleLocalImports(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("GO_SURGEON_ROOT", tmpDir)

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "go.mod"),
		[]byte("module example.com/multi\n\ngo 1.26\n"), 0644))

	for _, pkg := range []string{"sbx", "fleet"} {
		require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "internal", pkg), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "internal", pkg, pkg+".go"),
			[]byte("package "+pkg+"\n\nfunc Ready() bool { return true }\n"), 0644))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "internal", "app"), 0755))

	fs := filesystem.NewFileSystem()
	src := []byte("package app\n\nfunc F() bool { return sbx.Ready() && fleet.Ready() }\n")

	added, err := fs.WriteFile(context.Background(), "internal/app/app.go", src)
	require.NoError(t, err)

	written, err := os.ReadFile(filepath.Join(tmpDir, "internal", "app", "app.go"))
	require.NoError(t, err)
	assert.Contains(t, string(written), "example.com/multi/internal/sbx")
	assert.Contains(t, string(written), "example.com/multi/internal/fleet")
	assert.Contains(t, added, "example.com/multi/internal/sbx")
	assert.Contains(t, added, "example.com/multi/internal/fleet")
}
