package queries_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/queries"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupGraphFixture(t *testing.T) (string, *mockFS) {
	t.Helper()
	tmpDir := t.TempDir()

	// Package: pkg/domain
	domainDir := filepath.Join(tmpDir, "pkg", "domain")
	require.NoError(t, os.MkdirAll(domainDir, 0755))

	bookCode := `package domain

// Book represents a book entity.
type Book struct {
	ID     BookID
	Title  string
	Author string
}

// BookID is a typed identifier.
type BookID string

// NewBook creates a new Book.
func NewBook(title, author string) (*Book, error) {
	return &Book{Title: title, Author: author}, nil
}

func helperFunc() {}
`
	errorsCode := `package domain

// Error is a domain error.
type Error struct {
	Code    string
	Message string
	Err     error
}

// Error implements the error interface.
func (e *Error) Error() string {
	return e.Message
}

// Unwrap returns the underlying error.
func (e *Error) Unwrap() error {
	return e.Err
}
`
	require.NoError(t, os.WriteFile(filepath.Join(domainDir, "book.go"), []byte(bookCode), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(domainDir, "errors.go"), []byte(errorsCode), 0644))
	// Test file should be skipped
	require.NoError(t, os.WriteFile(filepath.Join(domainDir, "book_test.go"), []byte("package domain\n"), 0644))

	// Package: pkg/domain/repositories
	repoDir := filepath.Join(tmpDir, "pkg", "domain", "repositories")
	require.NoError(t, os.MkdirAll(repoDir, 0755))

	repoCode := `package repositories

type BookRepository interface {
	Create(ctx interface{}, book interface{}) error
	FindByID(ctx interface{}, id string) (interface{}, error)
	Delete(ctx interface{}, id string) error
}
`
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "book.go"), []byte(repoCode), 0644))

	// Package: pkg/app
	appDir := filepath.Join(tmpDir, "pkg", "app")
	require.NoError(t, os.MkdirAll(appDir, 0755))

	appCode := `package app

type LargeConfig struct {
	Host     string
	Port     int
	Database string
	Username string
	Password string
	Timeout  int
}
`
	require.NoError(t, os.WriteFile(filepath.Join(appDir, "config.go"), []byte(appCode), 0644))

	// Hidden dir should be skipped
	hiddenDir := filepath.Join(tmpDir, ".hidden")
	require.NoError(t, os.MkdirAll(hiddenDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(hiddenDir, "skip.go"), []byte("package hidden\n"), 0644))

	// Vendor dir should be skipped
	vendorDir := filepath.Join(tmpDir, "vendor")
	require.NoError(t, os.MkdirAll(vendorDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(vendorDir, "skip.go"), []byte("package vendor\n"), 0644))

	files := loadFiles(t, tmpDir)
	return tmpDir, &mockFS{files: files}
}

// loadFiles walks dir and returns a map of path → content for all non-directory files.
func loadFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, _ := os.ReadFile(path)
		files[path] = data
		return nil
	})
	require.NoError(t, err)
	return files
}

// --- Package listing (symbols=false) ---

func TestGraph_PackagesOnly(t *testing.T) {
	tmpDir, fs := setupGraphFixture(t)
	handler := queries.NewSurgeonQueriesHandler(fs)

	packages, err := handler.Graph(context.Background(), tmpDir, false, false, false, false, false)
	require.NoError(t, err)

	var paths []string
	for _, p := range packages {
		paths = append(paths, p.Path)
	}

	assert.Len(t, packages, 3)
	assert.Contains(t, paths, filepath.Join(tmpDir, "pkg", "app"))
	assert.Contains(t, paths, filepath.Join(tmpDir, "pkg", "domain"))
	assert.Contains(t, paths, filepath.Join(tmpDir, "pkg", "domain", "repositories"))

	for _, p := range paths {
		assert.NotContains(t, p, ".hidden")
		assert.NotContains(t, p, "vendor")
	}

	for _, pkg := range packages {
		assert.Nil(t, pkg.Files)
	}
}

func TestGraph_EmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	handler := queries.NewSurgeonQueriesHandler(&mockFS{files: map[string][]byte{}})

	packages, err := handler.Graph(context.Background(), tmpDir, false, false, false, false, false)
	require.NoError(t, err)
	assert.Empty(t, packages)
}

// --- Symbols: non-recursive (default) ---

func TestGraph_WithSymbols_NonRecursive(t *testing.T) {
	tmpDir, fs := setupGraphFixture(t)
	handler := queries.NewSurgeonQueriesHandler(fs)

	domainDir := filepath.Join(tmpDir, "pkg", "domain")
	packages, err := handler.Graph(context.Background(), domainDir, true, false, false, false, false)
	require.NoError(t, err)

	// Only the target directory — repositories sub-package is excluded.
	require.Len(t, packages, 1)
	assert.Equal(t, domainDir, packages[0].Path)

	require.Len(t, packages[0].Files, 2) // book.go, errors.go (book_test.go skipped)

	bookFile := packages[0].Files[0]
	assert.True(t, strings.HasSuffix(bookFile.Path, "book.go"))
	require.Len(t, bookFile.Symbols, 3)
	assert.Equal(t, "type Book struct { ID BookID; Title string; Author string }", bookFile.Symbols[0])
	assert.Equal(t, "type BookID string", bookFile.Symbols[1])
	assert.Equal(t, "func NewBook(title, author string) (*Book, error)", bookFile.Symbols[2])

	errFile := packages[0].Files[1]
	assert.True(t, strings.HasSuffix(errFile.Path, "errors.go"))
	require.Len(t, errFile.Symbols, 3)
	assert.Equal(t, "type Error struct { Code string; Message string; Err error }", errFile.Symbols[0])
	assert.Equal(t, "func (e *Error) Error() string", errFile.Symbols[1])
	assert.Equal(t, "func (e *Error) Unwrap() error", errFile.Symbols[2])
}

func TestGraph_WithSymbols_NonRecursive_NoSubDirFiles(t *testing.T) {
	tmpDir, fs := setupGraphFixture(t)
	handler := queries.NewSurgeonQueriesHandler(fs)

	repoDir := filepath.Join(tmpDir, "pkg", "domain", "repositories")
	packages, err := handler.Graph(context.Background(), repoDir, true, false, false, false, false)
	require.NoError(t, err)
	require.Len(t, packages, 1)
	require.Len(t, packages[0].Files, 1)
	assert.Equal(t, "type BookRepository interface { Create; FindByID; Delete }", packages[0].Files[0].Symbols[0])
}

// --- Symbols: recursive (opt-in) ---

func TestGraph_WithSymbols_Recursive(t *testing.T) {
	tmpDir, fs := setupGraphFixture(t)
	handler := queries.NewSurgeonQueriesHandler(fs)

	domainDir := filepath.Join(tmpDir, "pkg", "domain")
	packages, err := handler.Graph(context.Background(), domainDir, true, false, false, true, false)
	require.NoError(t, err)

	require.Len(t, packages, 2)
	assert.Equal(t, domainDir, packages[0].Path)
	assert.Equal(t, filepath.Join(domainDir, "repositories"), packages[1].Path)
}

func TestGraph_InterfaceFormatting(t *testing.T) {
	tmpDir, fs := setupGraphFixture(t)
	handler := queries.NewSurgeonQueriesHandler(fs)

	repoDir := filepath.Join(tmpDir, "pkg", "domain", "repositories")
	packages, err := handler.Graph(context.Background(), repoDir, true, false, false, false, false)
	require.NoError(t, err)
	require.Len(t, packages, 1)
	require.Len(t, packages[0].Files, 1)

	symbols := packages[0].Files[0].Symbols
	require.Len(t, symbols, 1)
	assert.Equal(t, "type BookRepository interface { Create; FindByID; Delete }", symbols[0])
}

func TestGraph_LargeStructMultiLine(t *testing.T) {
	tmpDir, fs := setupGraphFixture(t)
	handler := queries.NewSurgeonQueriesHandler(fs)

	appDir := filepath.Join(tmpDir, "pkg", "app")
	packages, err := handler.Graph(context.Background(), appDir, true, false, false, false, false)
	require.NoError(t, err)
	require.Len(t, packages, 1)
	require.Len(t, packages[0].Files, 1)

	symbols := packages[0].Files[0].Symbols
	require.Len(t, symbols, 1)

	expected := "type LargeConfig struct {\n" +
		"    Host string\n" +
		"    Port int\n" +
		"    Database string\n" +
		"    Username string\n" +
		"    Password string\n" +
		"    Timeout int\n" +
		"}"
	assert.Equal(t, expected, symbols[0])
}

// --- Tests flag ---

func setupTestsFixture(t *testing.T) (string, *mockFS) {
	t.Helper()
	tmpDir := t.TempDir()

	pkgDir := filepath.Join(tmpDir, "pkg", "commands")
	require.NoError(t, os.MkdirAll(pkgDir, 0755))

	// Production file: exported symbols only.
	prodCode := `package commands

type CreateHandler struct{}

func NewCreateHandler() *CreateHandler { return &CreateHandler{} }
func (h *CreateHandler) Handle() error  { return nil }
`

	// Test file: exported test funcs + unexported helpers + unexported type.
	testCode := `package commands

import "testing"

type mockDep struct{ called bool }

func setupHandler(t *testing.T) (*CreateHandler, *mockDep) {
	t.Helper()
	return NewCreateHandler(), &mockDep{}
}

func TestHandle_Success(t *testing.T) {}
func TestHandle_Error(t *testing.T)   {}
`

	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "create.go"), []byte(prodCode), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "create_test.go"), []byte(testCode), 0644))

	files := loadFiles(t, tmpDir)
	return tmpDir, &mockFS{files: files}
}

func TestGraph_WithTests_ExcludesTestFilesByDefault(t *testing.T) {
	tmpDir, fs := setupTestsFixture(t)
	handler := queries.NewSurgeonQueriesHandler(fs)

	pkgDir := filepath.Join(tmpDir, "pkg", "commands")
	packages, err := handler.Graph(context.Background(), pkgDir, true, false, false, false, false)
	require.NoError(t, err)
	require.Len(t, packages, 1)

	// Only create.go — test file excluded.
	require.Len(t, packages[0].Files, 1)
	assert.True(t, strings.HasSuffix(packages[0].Files[0].Path, "create.go"))
}

func TestGraph_WithTests_IncludesTestFiles(t *testing.T) {
	tmpDir, fs := setupTestsFixture(t)
	handler := queries.NewSurgeonQueriesHandler(fs)

	pkgDir := filepath.Join(tmpDir, "pkg", "commands")
	packages, err := handler.Graph(context.Background(), pkgDir, true, false, false, false, true)
	require.NoError(t, err)
	require.Len(t, packages, 1)

	// Both create.go and create_test.go.
	require.Len(t, packages[0].Files, 2)
	assert.True(t, strings.HasSuffix(packages[0].Files[0].Path, "create.go"))
	assert.True(t, strings.HasSuffix(packages[0].Files[1].Path, "create_test.go"))
}

func TestGraph_WithTests_UnexportedSymbolsInTestFile(t *testing.T) {
	tmpDir, fs := setupTestsFixture(t)
	handler := queries.NewSurgeonQueriesHandler(fs)

	pkgDir := filepath.Join(tmpDir, "pkg", "commands")
	packages, err := handler.Graph(context.Background(), pkgDir, true, false, false, false, true)
	require.NoError(t, err)
	require.Len(t, packages, 1)

	// Find the test file's symbols.
	var testFileSymbols []string
	for _, f := range packages[0].Files {
		if strings.HasSuffix(f.Path, "_test.go") {
			testFileSymbols = f.Symbols
		}
	}
	require.NotNil(t, testFileSymbols)

	symNames := make(map[string]bool)
	for _, s := range testFileSymbols {
		symNames[s] = true
	}

	// Unexported type and function are visible.
	assert.True(t, symNames["type mockDep struct { called bool }"], "mockDep type should be visible")
	assert.Contains(t, strings.Join(testFileSymbols, "\n"), "func setupHandler")

	// Exported test functions are visible.
	assert.Contains(t, strings.Join(testFileSymbols, "\n"), "func TestHandle_Success")
	assert.Contains(t, strings.Join(testFileSymbols, "\n"), "func TestHandle_Error")
}

func TestGraph_WithTests_ProductionFileUnexportedStillHidden(t *testing.T) {
	tmpDir, fs := setupGraphFixture(t)
	handler := queries.NewSurgeonQueriesHandler(fs)

	// helperFunc is unexported in book.go (a production file).
	// --tests should NOT expose it — unexported-in-test-files logic is test-file only.
	domainDir := filepath.Join(tmpDir, "pkg", "domain")
	packages, err := handler.Graph(context.Background(), domainDir, true, false, false, false, true)
	require.NoError(t, err)
	require.Len(t, packages, 1)

	// Production files still only show exported symbols.
	for _, f := range packages[0].Files {
		if strings.HasSuffix(f.Path, "book.go") {
			for _, sym := range f.Symbols {
				assert.NotContains(t, sym, "helperFunc", "unexported production func should remain hidden")
			}
		}
	}
}

// --- Summary tests ---

func setupSummaryFixture(t *testing.T) (string, *mockFS) {
	t.Helper()
	tmpDir := t.TempDir()

	withDocDir := filepath.Join(tmpDir, "pkg", "domain")
	require.NoError(t, os.MkdirAll(withDocDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(withDocDir, "domain.go"), []byte(`// Package domain provides core entities and business rules.
package domain

type Book struct{}
`), 0644))

	withDocGoDir := filepath.Join(tmpDir, "pkg", "app")
	require.NoError(t, os.MkdirAll(withDocGoDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(withDocGoDir, "aaa.go"), []byte(`// Package app wrong description from aaa.go.
package app
`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(withDocGoDir, "doc.go"), []byte(`// Package app provides CQRS use case handlers.
package app
`), 0644))

	noDocDir := filepath.Join(tmpDir, "pkg", "infra")
	require.NoError(t, os.MkdirAll(noDocDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(noDocDir, "infra.go"), []byte(`package infra

type DB struct{}
`), 0644))

	bareDir := filepath.Join(tmpDir, "pkg", "bare")
	require.NoError(t, os.MkdirAll(bareDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(bareDir, "bare.go"), []byte(`// Package bare
package bare
`), 0644))

	files := loadFiles(t, tmpDir)
	return tmpDir, &mockFS{files: files}
}

func TestGraph_Summary_ExtractsDocComment(t *testing.T) {
	tmpDir, fs := setupSummaryFixture(t)
	handler := queries.NewSurgeonQueriesHandler(fs)

	packages, err := handler.Graph(context.Background(), tmpDir, false, true, false, false, false)
	require.NoError(t, err)
	require.Len(t, packages, 4)

	byPath := make(map[string]string)
	for _, p := range packages {
		byPath[p.Path] = p.Summary
	}

	assert.Equal(t, "provides core entities and business rules.", byPath[filepath.Join(tmpDir, "pkg", "domain")])
	assert.Equal(t, "provides CQRS use case handlers.", byPath[filepath.Join(tmpDir, "pkg", "app")])
	assert.Equal(t, "", byPath[filepath.Join(tmpDir, "pkg", "infra")])
	assert.Equal(t, "", byPath[filepath.Join(tmpDir, "pkg", "bare")])
}

func TestGraph_Summary_DocGoPriority(t *testing.T) {
	tmpDir, fs := setupSummaryFixture(t)
	handler := queries.NewSurgeonQueriesHandler(fs)

	packages, err := handler.Graph(context.Background(), tmpDir, false, true, false, false, false)
	require.NoError(t, err)

	for _, p := range packages {
		if p.Path == filepath.Join(tmpDir, "pkg", "app") {
			assert.Equal(t, "provides CQRS use case handlers.", p.Summary)
			return
		}
	}
	t.Fatal("pkg/app not found")
}

// --- Deps tests ---

func setupDepsFixture(t *testing.T) (string, *mockFS) {
	t.Helper()
	tmpDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module example.com/myapp\n\ngo 1.21\n"), 0644))

	domainDir := filepath.Join(tmpDir, "pkg", "domain")
	require.NoError(t, os.MkdirAll(domainDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(domainDir, "domain.go"), []byte(`package domain

type Book struct{}
`), 0644))

	appDir := filepath.Join(tmpDir, "pkg", "app")
	require.NoError(t, os.MkdirAll(appDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(appDir, "service.go"), []byte(`package app

import (
	"context"

	"example.com/myapp/pkg/domain"
)

func Handle(ctx context.Context, b domain.Book) {}
`), 0644))

	infraDir := filepath.Join(tmpDir, "pkg", "infra")
	require.NoError(t, os.MkdirAll(infraDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(infraDir, "infra.go"), []byte(`package infra

import (
	"example.com/myapp/pkg/domain"
	"example.com/myapp/pkg/app"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

var _ = domain.Book{}
var _ = app.Handle
`), 0644))

	files := loadFiles(t, tmpDir)
	return tmpDir, &mockFS{files: files}
}

func TestGraph_Deps_InternalImports(t *testing.T) {
	tmpDir, fs := setupDepsFixture(t)
	handler := queries.NewSurgeonQueriesHandler(fs)

	packages, err := handler.Graph(context.Background(), tmpDir, false, false, true, false, false)
	require.NoError(t, err)
	require.Len(t, packages, 3)

	byPath := make(map[string][]string)
	for _, p := range packages {
		byPath[p.Path] = p.Deps
	}

	assert.Nil(t, byPath[filepath.Join(tmpDir, "pkg", "domain")])
	assert.Equal(t, []string{"pkg/domain"}, byPath[filepath.Join(tmpDir, "pkg", "app")])
	assert.Equal(t, []string{"pkg/app", "pkg/domain"}, byPath[filepath.Join(tmpDir, "pkg", "infra")])
}

func TestGraph_Deps_NoGoMod(t *testing.T) {
	tmpDir, fs := setupGraphFixture(t)
	handler := queries.NewSurgeonQueriesHandler(fs)

	packages, err := handler.Graph(context.Background(), tmpDir, false, false, true, false, false)
	require.NoError(t, err)
	for _, p := range packages {
		assert.Nil(t, p.Deps)
	}
}
