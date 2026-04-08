package commands_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMockFromSource_Simple(t *testing.T) {
	tmpDir := t.TempDir()
	ifaceDir := filepath.Join(tmpDir, "repo")
	mockDir := filepath.Join(tmpDir, "repotest")
	ifaceFile := filepath.Join(ifaceDir, "repo.go")
	mockFile := filepath.Join(mockDir, "mock.go")

	fs := &mockFS{
		files: map[string][]byte{
			filepath.Join(ifaceDir, "existing.go"): []byte("package repo\n"),
			filepath.Join(mockDir, "existing.go"):  []byte("package repotest\n"),
		},
	}
	handler := commands.NewExecutePlanHandler(fs)

	src := `type BookRepository interface {
	Create(ctx context.Context, id string) error
	FindByID(ctx context.Context, id string) (*Book, error)
}`

	result, err := handler.MockFromSource(context.Background(), src, "MockBookRepository", mockFile, ifaceFile)
	require.NoError(t, err)
	assert.Contains(t, result, "MockBookRepository")
	assert.Contains(t, result, "2 methods")

	content := string(fs.files[mockFile])

	// Package
	assert.Contains(t, content, "package repotest")

	// Struct
	assert.Contains(t, content, "type MockBookRepository struct {")
	assert.Contains(t, content, "CreateFunc func(ctx context.Context, id string) error")
	assert.Contains(t, content, "FindByIDFunc func(ctx context.Context, id string) (*Book, error)")

	// Delegation methods
	assert.Contains(t, content, "func (m *MockBookRepository) Create(ctx context.Context, id string) error {")
	assert.Contains(t, content, "func (m *MockBookRepository) FindByID(ctx context.Context, id string) (*Book, error) {")

	// Nil panics
	assert.Contains(t, content, `panic("MockBookRepository.CreateFunc not set")`)
	assert.Contains(t, content, `panic("MockBookRepository.FindByIDFunc not set")`)

	// Delegation calls
	assert.Contains(t, content, "return m.CreateFunc(ctx, id)")
	assert.Contains(t, content, "return m.FindByIDFunc(ctx, id)")

	// Compile-time check (different packages)
	assert.Contains(t, content, "var _ repo.BookRepository = (*MockBookRepository)(nil)")
}

func TestMockFromSource_SamePackage(t *testing.T) {
	tmpDir := t.TempDir()
	ifaceFile := filepath.Join(tmpDir, "repo.go")
	mockFile := filepath.Join(tmpDir, "mock.go")

	fs := &mockFS{
		files: map[string][]byte{
			filepath.Join(tmpDir, "existing.go"): []byte("package repo\n"),
		},
	}
	handler := commands.NewExecutePlanHandler(fs)

	src := `type Reader interface {
	Read(p []byte) (n int, err error)
}`

	_, err := handler.MockFromSource(context.Background(), src, "MockReader", mockFile, ifaceFile)
	require.NoError(t, err)

	content := string(fs.files[mockFile])
	// Same package: no package qualifier in compile-time check
	assert.Contains(t, content, "var _ Reader = (*MockReader)(nil)")
}

func TestMockFromSource_VoidMethod(t *testing.T) {
	tmpDir := t.TempDir()
	ifaceFile := filepath.Join(tmpDir, "svc.go")
	mockFile := filepath.Join(tmpDir, "mock.go")

	fs := &mockFS{files: map[string][]byte{}}
	handler := commands.NewExecutePlanHandler(fs)

	src := `type Logger interface {
	Log(msg string)
	Flush()
}`

	_, err := handler.MockFromSource(context.Background(), src, "MockLogger", mockFile, ifaceFile)
	require.NoError(t, err)

	content := string(fs.files[mockFile])
	// No return for void methods
	assert.Contains(t, content, "m.LogFunc(msg)")
	assert.NotContains(t, content, "return m.LogFunc")
	assert.Contains(t, content, "m.FlushFunc()")
}

func TestMockFromSource_NoInterface(t *testing.T) {
	tmpDir := t.TempDir()
	fs := &mockFS{files: map[string][]byte{}}
	handler := commands.NewExecutePlanHandler(fs)

	src := `type Book struct { Title string }`

	_, err := handler.MockFromSource(context.Background(), src, "MockBook",
		filepath.Join(tmpDir, "mock.go"), filepath.Join(tmpDir, "book.go"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no interface type declaration found")
}

func TestMockFromSource_UnnamedParams(t *testing.T) {
	tmpDir := t.TempDir()
	ifaceFile := filepath.Join(tmpDir, "svc.go")
	mockFile := filepath.Join(tmpDir, "mock.go")

	fs := &mockFS{files: map[string][]byte{}}
	handler := commands.NewExecutePlanHandler(fs)

	src := `type Converter interface {
	Convert(string, int) bool
}`

	_, err := handler.MockFromSource(context.Background(), src, "MockConverter", mockFile, ifaceFile)
	require.NoError(t, err)

	content := string(fs.files[mockFile])
	// Unnamed params get p0, p1, ...
	assert.Contains(t, content, "p0 string, p1 int")
	assert.Contains(t, content, "m.ConvertFunc(p0, p1)")
}
