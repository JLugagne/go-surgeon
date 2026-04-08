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

	files := make(map[string][]byte)
	filepath.Walk(tmpDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, _ := os.ReadFile(path)
		files[path] = data
		return nil
	})

	return tmpDir, &mockFS{files: files}
}

func TestGraph_PackagesOnly(t *testing.T) {
	tmpDir, fs := setupGraphFixture(t)
	handler := queries.NewSurgeonQueriesHandler(fs)

	packages, err := handler.Graph(context.Background(), tmpDir, false)
	require.NoError(t, err)

	var paths []string
	for _, p := range packages {
		paths = append(paths, p.Path)
	}

	// Should contain our 3 packages
	assert.Len(t, packages, 3)
	assert.Contains(t, paths, filepath.Join(tmpDir, "pkg", "app"))
	assert.Contains(t, paths, filepath.Join(tmpDir, "pkg", "domain"))
	assert.Contains(t, paths, filepath.Join(tmpDir, "pkg", "domain", "repositories"))

	// Should NOT contain hidden or vendor
	for _, p := range paths {
		assert.NotContains(t, p, ".hidden")
		assert.NotContains(t, p, "vendor")
	}

	// Files should be empty when symbols=false
	for _, pkg := range packages {
		assert.Nil(t, pkg.Files)
	}
}

func TestGraph_WithSymbols(t *testing.T) {
	tmpDir, fs := setupGraphFixture(t)
	handler := queries.NewSurgeonQueriesHandler(fs)

	domainDir := filepath.Join(tmpDir, "pkg", "domain")
	packages, err := handler.Graph(context.Background(), domainDir, true)
	require.NoError(t, err)

	// Should find domain and domain/repositories
	require.Len(t, packages, 2)

	// First package: domain
	domainPkg := packages[0]
	assert.Equal(t, domainDir, domainPkg.Path)
	require.Len(t, domainPkg.Files, 2) // book.go, errors.go (not book_test.go)

	// book.go symbols
	bookFile := domainPkg.Files[0]
	assert.True(t, strings.HasSuffix(bookFile.Path, "book.go"))
	require.Len(t, bookFile.Symbols, 3) // Book struct, BookID type, NewBook func (helperFunc is unexported)

	// Book struct — compact (3 fields)
	assert.Equal(t, "type Book struct { ID BookID; Title string; Author string }", bookFile.Symbols[0])

	// BookID type alias
	assert.Equal(t, "type BookID string", bookFile.Symbols[1])

	// NewBook function signature
	assert.Equal(t, "func NewBook(title, author string) (*Book, error)", bookFile.Symbols[2])

	// errors.go symbols
	errFile := domainPkg.Files[1]
	assert.True(t, strings.HasSuffix(errFile.Path, "errors.go"))
	require.Len(t, errFile.Symbols, 3) // Error struct, Error method, Unwrap method

	// Error struct
	assert.Equal(t, "type Error struct { Code string; Message string; Err error }", errFile.Symbols[0])

	// Methods
	assert.Equal(t, "func (e *Error) Error() string", errFile.Symbols[1])
	assert.Equal(t, "func (e *Error) Unwrap() error", errFile.Symbols[2])
}

func TestGraph_InterfaceFormatting(t *testing.T) {
	tmpDir, fs := setupGraphFixture(t)
	handler := queries.NewSurgeonQueriesHandler(fs)

	repoDir := filepath.Join(tmpDir, "pkg", "domain", "repositories")
	packages, err := handler.Graph(context.Background(), repoDir, true)
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
	packages, err := handler.Graph(context.Background(), appDir, true)
	require.NoError(t, err)
	require.Len(t, packages, 1)
	require.Len(t, packages[0].Files, 1)

	symbols := packages[0].Files[0].Symbols
	require.Len(t, symbols, 1)

	// 6 fields > 5, should be multi-line
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

func TestGraph_EmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	handler := queries.NewSurgeonQueriesHandler(&mockFS{files: map[string][]byte{}})

	packages, err := handler.Graph(context.Background(), tmpDir, false)
	require.NoError(t, err)
	assert.Empty(t, packages)
}
