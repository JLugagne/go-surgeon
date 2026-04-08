package queries_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestFindSymbols_DefaultExcludesTestFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Production file with a function.
	prodPath := filepath.Join(tmpDir, "service.go")
	require.NoError(t, os.WriteFile(prodPath, []byte(`package svc

func ProdFunc() {}
`), 0644))

	// Test file with a helper function.
	testPath := filepath.Join(tmpDir, "service_test.go")
	require.NoError(t, os.WriteFile(testPath, []byte(`package svc

import "testing"

func setupTest(t *testing.T) {}
func TestProdFunc(t *testing.T) {}
`), 0644))

	fs := &mockFS{
		files: map[string][]byte{
			prodPath: mustReadFile(t, prodPath),
			testPath: mustReadFile(t, testPath),
		},
	}
	handler := queries.NewSurgeonQueriesHandler(fs)

	// Without --tests: test helpers are invisible.
	res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{Name: "setupTest"}, tmpDir)
	require.NoError(t, err)
	assert.Empty(t, res)

	res, err = handler.FindSymbols(context.Background(), domain.SymbolQuery{Name: "TestProdFunc"}, tmpDir)
	require.NoError(t, err)
	assert.Empty(t, res)

	// Production file is still found.
	res, err = handler.FindSymbols(context.Background(), domain.SymbolQuery{Name: "ProdFunc"}, tmpDir)
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, "ProdFunc", res[0].Name)
}

func TestFindSymbols_WithTests_FindsTestHelpers(t *testing.T) {
	tmpDir := t.TempDir()

	prodPath := filepath.Join(tmpDir, "service.go")
	require.NoError(t, os.WriteFile(prodPath, []byte(`package svc

func ProdFunc() {}
`), 0644))

	testPath := filepath.Join(tmpDir, "service_test.go")
	require.NoError(t, os.WriteFile(testPath, []byte(`package svc

import "testing"

func setupTest(t *testing.T) {}
func TestProdFunc(t *testing.T) {}
`), 0644))

	fs := &mockFS{
		files: map[string][]byte{
			prodPath: mustReadFile(t, prodPath),
			testPath: mustReadFile(t, testPath),
		},
	}
	handler := queries.NewSurgeonQueriesHandler(fs)

	// With --tests: unexported helper is found.
	res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{Name: "setupTest", Tests: true}, tmpDir)
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, "setupTest", res[0].Name)
	assert.True(t, strings.HasSuffix(res[0].File, "_test.go"))

	// With --tests: exported test function is also found.
	res, err = handler.FindSymbols(context.Background(), domain.SymbolQuery{Name: "TestProdFunc", Tests: true}, tmpDir)
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, "TestProdFunc", res[0].Name)
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}
