package queries_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/queries"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockFS struct {
	files map[string][]byte
}

func (m *mockFS) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if content, ok := m.files[path]; ok {
		return content, nil
	}
	return nil, os.ErrNotExist
}

func (m *mockFS) WriteFile(ctx context.Context, path string, content []byte) error { return nil }
func (m *mockFS) ReadDir(ctx context.Context, path string) ([]string, error)       { return nil, nil }
func (m *mockFS) IsDir(ctx context.Context, path string) (bool, error)             { return false, nil }
func (m *mockFS) MkdirAll(ctx context.Context, path string) error                  { return nil }
func (m *mockFS) ExecuteGoImports(ctx context.Context, files []string) error       { return nil }

func TestFindSymbols(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.go")
	
	code := `package testpkg

// MyStruct is a test struct.
type MyStruct struct {
	Field1 string
}

// DoWork does work.
func (m *MyStruct) DoWork() error {
	
	return nil
}

// FreeFunc is free.
func FreeFunc() {
}
`
	err := os.WriteFile(filePath, []byte(code), 0644)
	require.NoError(t, err)

	fs := &mockFS{
		files: map[string][]byte{
			filePath: []byte(code),
		},
	}
	handler := queries.NewSurgeonQueriesHandler(fs)

	t.Run("Find Struct", func(t *testing.T) {
		res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{Name: "MyStruct"}, tmpDir)
		require.NoError(t, err)
		require.Len(t, res, 1)
		
		assert.Equal(t, "MyStruct", res[0].Name)
		assert.Equal(t, "MyStruct is a test struct.", res[0].Doc)
		assert.Contains(t, res[0].Signature, "MyStruct")
		assert.Equal(t, 3, res[0].LineStart)
		assert.Equal(t, 6, res[0].LineEnd)
		assert.Contains(t, res[0].Code, "3: // MyStruct is a test struct.")
		assert.Contains(t, res[0].Code, "4: type MyStruct struct {")
	})

	t.Run("Find Method", func(t *testing.T) {
		res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{Receiver: "MyStruct", Name: "DoWork"}, tmpDir)
		require.NoError(t, err)
		require.Len(t, res, 1)

		assert.Equal(t, "DoWork", res[0].Name)
		assert.Equal(t, "MyStruct", res[0].Receiver)
		assert.Equal(t, "DoWork does work.", res[0].Doc)
		assert.Contains(t, res[0].Signature, "func (m *MyStruct) DoWork() error")
		assert.Equal(t, 9, res[0].LineStart)
		assert.Equal(t, 12, res[0].LineEnd)
		// Empty line should be stripped
		assert.NotContains(t, res[0].Code, "10: \n")
		assert.Contains(t, res[0].Code, "9: func (m *MyStruct) DoWork() error {")
		assert.Contains(t, res[0].Code, "11: \treturn nil")
	})

	t.Run("Find Function", func(t *testing.T) {
		res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{Name: "FreeFunc"}, tmpDir)
		require.NoError(t, err)
		require.Len(t, res, 1)

		assert.Equal(t, "FreeFunc", res[0].Name)
		assert.Empty(t, res[0].Receiver)
		assert.Equal(t, "FreeFunc is free.", res[0].Doc)
	})
}
