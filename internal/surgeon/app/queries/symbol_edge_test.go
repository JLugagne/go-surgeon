package queries_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFindSymbols_BodylessFunc asserts a body-less function declaration
// (assembly/linkname stub — legal Go) is returned instead of panicking on
// the nil *ast.BlockStmt dereference in extractFuncResult.
func TestFindSymbols_BodylessFunc(t *testing.T) {
	tmpDir := t.TempDir()
	code := "package mathx\n\n// add is implemented in assembly.\nfunc add(x, y int64) int64\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "add.go"), []byte(code), 0o644))

	h := newHandler(t)
	results, err := h.FindSymbols(context.Background(), domain.SymbolQuery{Name: "add"}, tmpDir)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Contains(t, results[0].Signature, "func add(x, y int64) int64")
}

// TestFindSymbols_GenericReceiverMethod asserts methods on generic types
// (func (s *Store[T]) Get()) are findable via Receiver.Method queries.
func TestFindSymbols_GenericReceiverMethod(t *testing.T) {
	tmpDir := t.TempDir()
	code := "package store\n\ntype Store[T any] struct{ items []T }\n\nfunc (s *Store[T]) Get(i int) T {\n\treturn s.items[i]\n}\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "store.go"), []byte(code), 0o644))

	h := newHandler(t)
	results, err := h.FindSymbols(context.Background(), domain.SymbolQuery{Receiver: "Store", Name: "Get"}, tmpDir)
	require.NoError(t, err)
	require.Len(t, results, 1, "generic receiver method must be findable")
	assert.Equal(t, "Get", results[0].Name)
}
