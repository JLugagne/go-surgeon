package commands_test

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/outbound/filesystem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMockFromSource_VariadicMethod asserts variadic params survive mock
// generation. Before the fix the *ast.Ellipsis source ("...T") got another
// "..." prefixed (the code assumed "[]T"), emitting "opts ......Option" —
// an unparseable file that was still written.
func TestMockFromSource_VariadicMethod(t *testing.T) {
	tmpDir := t.TempDir()
	ifacePath := filepath.Join(tmpDir, "notifier.go")
	mockPath := filepath.Join(tmpDir, "mock_notifier.go")
	require.NoError(t, os.WriteFile(ifacePath, []byte("package notify\n\ntype Option struct{}\n"), 0o644))

	fs := &mockFS{files: map[string][]byte{ifacePath: []byte("package notify\n\ntype Option struct{}\n")}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.MockFromSource(context.Background(),
		"type Notifier interface {\n\tSend(msg string, opts ...Option) error\n}",
		"MockNotifier", mockPath, ifacePath)
	require.NoError(t, err)

	mockSrc := string(fs.files[mockPath])
	assert.Contains(t, mockSrc, "opts ...Option", "variadic param must keep a single ellipsis")
	assert.NotContains(t, mockSrc, "......", "double ellipsis is unparseable")
	_, perr := parser.ParseFile(token.NewFileSet(), "mock.go", mockSrc, 0)
	assert.NoError(t, perr, "generated mock must parse: %s", mockSrc)
}

// TestImplement_VariadicMethod asserts stubs keep the "..." of variadic
// interface methods. types.TypeString prints the tuple's last param as a
// slice, so before the fix the stub compiled but silently did NOT satisfy
// the interface.
func TestImplement_VariadicMethod(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notifier.go"), []byte("package testmod\n\ntype Notifier interface {\n\tSend(msg string, args ...string) error\n}\n"), 0o644))
	recvPath := filepath.Join(dir, "recv.go")
	require.NoError(t, os.WriteFile(recvPath, []byte("package testmod\n\ntype N struct{}\n"), 0o644))

	origWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(origWD) })
	t.Setenv("MCP_WORKTREE_ROOT", "")
	t.Setenv("GO_SURGEON_ROOT", dir)

	h := commands.NewExecutePlanHandler(filesystem.NewFileSystem())
	_, err = h.Implement(context.Background(), domain.ImplementRequest{
		Interface: "testmod.Notifier",
		Receiver:  "*N",
		FilePath:  recvPath,
	})
	require.NoError(t, err)

	updated, err := os.ReadFile(recvPath)
	require.NoError(t, err)
	assert.Contains(t, string(updated), "args ...string", "variadic must be preserved so the stub satisfies the interface; got:\n%s", string(updated))
}
