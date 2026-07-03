package commands_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	repofs "github.com/JLugagne/go-surgeon/internal/surgeon/domain/repositories/filesystem"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/outbound/filesystem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tamperReadFS wraps a real filesystem and corrupts the bytes returned
// by ReadFile for one file (matched by path suffix), so that the rename
// per-site text guard trips when that file is spliced. Writes and every
// other operation delegate to the real filesystem, so we can observe
// whether any file was written before the guard failure.
type tamperReadFS struct {
	repofs.FileSystem
	tamperSuffix string
}

func (f *tamperReadFS) ReadFile(ctx context.Context, path string) ([]byte, error) {
	b, err := f.FileSystem.ReadFile(ctx, path)
	if err == nil && strings.HasSuffix(path, f.tamperSuffix) {
		// Same-length swap keeps every collected offset valid but makes
		// working[offset:end] != oldName so the guard rejects the site.
		b = bytes.Replace(b, []byte("Greeter"), []byte("GreetXr"), 1)
	}
	return b, err
}

// TestRename_GuardFailureOnLaterFile_RollsBackEarlierFile pins the
// all-or-nothing contract: when a per-site text guard fails on a file
// processed later in the (sorted) file list, no earlier file may have
// been written to disk. lib/lib.go sorts before main.go, so with the
// pre-fix file-by-file writer lib/lib.go was corrupted before main.go's
// guard tripped. The fix buffers every rewritten file and validates all
// guards before any write.
func TestRename_GuardFailureOnLaterFile_RollsBackEarlierFile(t *testing.T) {
	dir := t.TempDir()
	writeRenameModule(t, dir)

	libPath := filepath.Join(dir, "lib", "lib.go")
	originalLib, err := os.ReadFile(libPath)
	require.NoError(t, err)

	fs := &tamperReadFS{FileSystem: filesystem.NewFileSystem(), tamperSuffix: "main.go"}
	handler := commands.NewExecutePlanHandler(fs)

	runInDir(t, dir, func() {
		_, err := handler.Rename(context.Background(), domain.RenameRequest{
			Symbol:  domain.SymbolRef{Name: "Greeter"},
			NewName: "Welcomer",
			Dir:     ".",
		})
		require.Error(t, err, "guard mismatch on main.go must fail the rename")

		libAfter, err := os.ReadFile(libPath)
		require.NoError(t, err)
		assert.Equal(t, string(originalLib), string(libAfter),
			"lib.go must stay byte-identical: a later-file guard failure must roll back")
	})
}
