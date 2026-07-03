package commands_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/outbound/filesystem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestImplement_SamePackageTypeNotQualified is a regression test for issue
// #18: Implement's qualifier always returned p.Name(), so a stub generated
// into the interface's own package referenced `testmod.ID` instead of `ID`,
// which does not compile. Foreign types (context.Context) must still be
// qualified and their imports resolved by goimports on write.
func TestImplement_SamePackageTypeNotQualified(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0o644))

	storeSrc := `package testmod

import "context"

type ID string

type Store interface {
	Fetch(ctx context.Context, id ID) (ID, error)
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "store.go"), []byte(storeSrc), 0o644))

	recvPath := filepath.Join(dir, "repo.go")
	require.NoError(t, os.WriteFile(recvPath, []byte("package testmod\n\ntype Repo struct{}\n"), 0o644))

	origWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(origWD) })
	t.Setenv("MCP_WORKTREE_ROOT", "")
	t.Setenv("GO_SURGEON_ROOT", dir)

	h := commands.NewExecutePlanHandler(filesystem.NewFileSystem())
	_, err = h.Implement(context.Background(), domain.ImplementRequest{
		Interface: "testmod.Store",
		Receiver:  "*Repo",
		FilePath:  recvPath,
	})
	require.NoError(t, err)

	updated, err := os.ReadFile(recvPath)
	require.NoError(t, err)
	got := string(updated)

	// Same-package type must NOT be package-qualified in the signature.
	// (The doc comment legitimately names the interface as testmod.Store.)
	assert.NotContains(t, got, "testmod.ID", "same-package types must not be qualified with the target package; got:\n%s", got)
	assert.Contains(t, got, "id ID", "same-package param type must be emitted bare; got:\n%s", got)
	assert.Contains(t, got, "(ID, error)", "same-package result type must be emitted bare; got:\n%s", got)

	// Foreign types stay qualified and goimports resolves their import.
	assert.Contains(t, got, "ctx context.Context", "foreign type must stay qualified; got:\n%s", got)
	assert.Contains(t, got, `"context"`, "goimports must add the foreign import; got:\n%s", got)
}
