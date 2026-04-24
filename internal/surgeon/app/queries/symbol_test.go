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

func (m *mockFS) WriteFile(ctx context.Context, path string, content []byte) ([]string, error) {
	return nil, nil
}
func (m *mockFS) ReadDir(ctx context.Context, path string) ([]string, error) { return nil, nil }
func (m *mockFS) IsDir(ctx context.Context, path string) (bool, error)       { return false, nil }
func (m *mockFS) MkdirAll(ctx context.Context, path string) error            { return nil }

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

	t.Run("Find with Package Name", func(t *testing.T) {
		res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{PackageName: "testpkg", Name: "FreeFunc"}, tmpDir)
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.Equal(t, "FreeFunc", res[0].Name)

		res2, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{PackageName: "wrongpkg", Name: "FreeFunc"}, tmpDir)
		require.NoError(t, err)
		require.Len(t, res2, 0)
	})
}

func TestFindSymbols_DefaultExcludesTestFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Production file with a function.
	prodPath := filepath.Join(tmpDir, "prod.go")
	prodCode := "package p\nfunc ProdFunc() {}"
	err := os.WriteFile(prodPath, []byte(prodCode), 0644)
	require.NoError(t, err)

	// Test file with a helper.
	testPath := filepath.Join(tmpDir, "prod_test.go")
	testCode := "package p\nfunc TestHelper() {}"
	err = os.WriteFile(testPath, []byte(testCode), 0644)
	require.NoError(t, err)

	fs := &mockFS{
		files: map[string][]byte{
			prodPath: []byte(prodCode),
			testPath: []byte(testCode),
		},
	}
	handler := queries.NewSurgeonQueriesHandler(fs)

	t.Run("Exclude test files by default", func(t *testing.T) {
		res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{Name: "TestHelper"}, tmpDir)
		require.NoError(t, err)
		assert.Len(t, res, 0)
	})

	t.Run("Include test files with --tests", func(t *testing.T) {
		res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{Name: "TestHelper", Tests: true}, tmpDir)
		require.NoError(t, err)
		assert.Len(t, res, 1)
		assert.Equal(t, "TestHelper", res[0].Name)
	})
}

func TestFindSymbols_Pattern(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.go")
	code := `package testpkg

type PatchFoo struct{}
type PatchBar struct{}
func (p *PatchFoo) Apply() error { return nil }
func HelperFunc() {}
`
	require.NoError(t, os.WriteFile(filePath, []byte(code), 0644))
	fs := &mockFS{files: map[string][]byte{filePath: []byte(code)}}
	handler := queries.NewSurgeonQueriesHandler(fs)

	t.Run("matches multiple declarations by regex", func(t *testing.T) {
		res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{Pattern: "^Patch"}, tmpDir)
		require.NoError(t, err)
		names := map[string]bool{}
		for _, r := range res {
			names[r.Name] = true
		}
		assert.True(t, names["PatchFoo"], "expected PatchFoo in results")
		assert.True(t, names["PatchBar"], "expected PatchBar in results")
		assert.False(t, names["HelperFunc"], "HelperFunc should not match ^Patch")
	})

	t.Run("invalid regex returns error", func(t *testing.T) {
		_, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{Pattern: "([unclosed"}, tmpDir)
		require.Error(t, err)
	})

	t.Run("no match returns empty", func(t *testing.T) {
		res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{Pattern: "^ZZZNotReal$"}, tmpDir)
		require.NoError(t, err)
		assert.Empty(t, res)
	})
}

func TestFindSymbols_VarsAndConsts(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "globals.go")
	code := `package testpkg

// MaxRetries bounds retry attempts.
const MaxRetries = 3

// ServerName identifies this instance.
var ServerName = "primary"

const (
	StatusOK     = 200
	StatusNotOK  = 500
)

var (
	logPrefix  = "[svc]"
	cacheSize  int
)
`
	err := os.WriteFile(filePath, []byte(code), 0644)
	require.NoError(t, err)

	fs := &mockFS{files: map[string][]byte{filePath: []byte(code)}}
	handler := queries.NewSurgeonQueriesHandler(fs)

	t.Run("finds a lone const", func(t *testing.T) {
		res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{Name: "MaxRetries"}, tmpDir)
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.Equal(t, "MaxRetries", res[0].Name)
		assert.Equal(t, "MaxRetries bounds retry attempts.", res[0].Doc)
		assert.Contains(t, res[0].Signature, "const MaxRetries")
	})

	t.Run("finds a lone var", func(t *testing.T) {
		res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{Name: "ServerName"}, tmpDir)
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.Equal(t, "ServerName", res[0].Name)
		assert.Contains(t, res[0].Signature, "var ServerName")
	})

	t.Run("finds a const inside a grouped block", func(t *testing.T) {
		res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{Name: "StatusOK"}, tmpDir)
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.Equal(t, "StatusOK", res[0].Name)
		assert.Contains(t, res[0].Signature, "const StatusOK")
	})

	t.Run("finds a var inside a grouped block", func(t *testing.T) {
		res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{Name: "cacheSize"}, tmpDir)
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.Equal(t, "cacheSize", res[0].Name)
		assert.Contains(t, res[0].Signature, "var cacheSize int")
	})

	t.Run("pattern matches across vars and consts", func(t *testing.T) {
		res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{Pattern: "^Status"}, tmpDir)
		require.NoError(t, err)
		require.Len(t, res, 2)
	})
}

// TestFindSymbols_Pattern_DocComment verifies that pattern-mode results
// carry the full doc comment so that downstream renderers (e.g. MCP
// outline mode) can derive a first-sentence summary. Also covers the
// negative case: a declaration without a doc comment yields an empty
// Doc string rather than failing.
func TestFindSymbols_Pattern_DocComment(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "registry.go")
	code := `package registry

// registerFoo wires Foo into the registry. Y is separate.
func registerFoo() {}

func registerBare() {}

// registerMulti describes the first sentence.
// The second line continues the first paragraph.
//
// A second paragraph should be ignored entirely.
func registerMulti() {}
`
	require.NoError(t, os.WriteFile(filePath, []byte(code), 0644))
	fs := &mockFS{files: map[string][]byte{filePath: []byte(code)}}
	handler := queries.NewSurgeonQueriesHandler(fs)

	res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{Pattern: "^register"}, tmpDir)
	require.NoError(t, err)
	byName := map[string]domain.SymbolResult{}
	for _, r := range res {
		byName[r.Name] = r
	}

	require.Contains(t, byName, "registerFoo")
	assert.Equal(t, "registerFoo wires Foo into the registry. Y is separate.", byName["registerFoo"].Doc)

	require.Contains(t, byName, "registerBare")
	assert.Empty(t, byName["registerBare"].Doc, "declaration without a doc comment should have empty Doc")

	require.Contains(t, byName, "registerMulti")
	assert.Contains(t, byName["registerMulti"].Doc, "registerMulti describes the first sentence.")
	assert.Contains(t, byName["registerMulti"].Doc, "A second paragraph should be ignored entirely.")
}

func TestFindSymbols_AtLine(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.go")
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

const MaxItems = 10
`
	err := os.WriteFile(filePath, []byte(code), 0644)
	require.NoError(t, err)

	fs := &mockFS{files: map[string][]byte{filePath: []byte(code)}}
	handler := queries.NewSurgeonQueriesHandler(fs)

	t.Run("line inside struct body", func(t *testing.T) {
		res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{File: filePath, AtLine: 5}, tmpDir)
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.Equal(t, "MyStruct", res[0].Name)
	})

	t.Run("line on struct declaration", func(t *testing.T) {
		res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{File: filePath, AtLine: 4}, tmpDir)
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.Equal(t, "MyStruct", res[0].Name)
	})

	t.Run("line inside method body", func(t *testing.T) {
		res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{File: filePath, AtLine: 11}, tmpDir)
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.Equal(t, "DoWork", res[0].Name)
		assert.Equal(t, "MyStruct", res[0].Receiver)
	})

	t.Run("line on free function signature", func(t *testing.T) {
		res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{File: filePath, AtLine: 15}, tmpDir)
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.Equal(t, "FreeFunc", res[0].Name)
		assert.Empty(t, res[0].Receiver)
	})

	t.Run("line on const declaration", func(t *testing.T) {
		res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{File: filePath, AtLine: 18}, tmpDir)
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.Equal(t, "MaxItems", res[0].Name)
	})

	t.Run("line outside any declaration returns empty", func(t *testing.T) {
		res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{File: filePath, AtLine: 2}, tmpDir)
		require.NoError(t, err)
		assert.Empty(t, res)
	})

	t.Run("missing file returns error", func(t *testing.T) {
		_, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{File: "", AtLine: 5}, tmpDir)
		require.Error(t, err)
	})
}

func (m *mockFS) DeleteFile(_ context.Context, _ string) error { return nil }

// TestFindSymbols_MaxResults verifies that MaxResults caps the result set in pattern mode.
func TestFindSymbols_MaxResults(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.go")
	code := `package testpkg

type FooA struct{}
type FooB struct{}
type FooC struct{}
`
	require.NoError(t, os.WriteFile(filePath, []byte(code), 0644))
	fs := &mockFS{files: map[string][]byte{filePath: []byte(code)}}
	handler := queries.NewSurgeonQueriesHandler(fs)

	res, err := handler.FindSymbols(context.Background(), domain.SymbolQuery{Pattern: "^Foo", MaxResults: 2}, tmpDir)
	require.NoError(t, err)
	assert.Len(t, res, 2, "MaxResults=2 must cap results at 2 even though 3 match")
}
