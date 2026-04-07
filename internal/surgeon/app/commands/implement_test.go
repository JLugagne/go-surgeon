package commands_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
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

func (m *mockFS) WriteFile(ctx context.Context, path string, content []byte) error {
	m.files[path] = content
	return nil
}

func (m *mockFS) ReadDir(ctx context.Context, path string) ([]string, error) {
	var names []string
	for k := range m.files {
		if filepath.Dir(k) == path {
			names = append(names, filepath.Base(k))
		}
	}
	return names, nil
}
func (m *mockFS) IsDir(ctx context.Context, path string) (bool, error)             { return false, nil }
func (m *mockFS) MkdirAll(ctx context.Context, path string) error                  { return nil }
func (m *mockFS) ExecuteGoImports(ctx context.Context, files []string) error       { return nil }

func TestImplement(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test_impl.go")

	initialCode := `package testpkg

type MyContext struct {}
`
	fs := &mockFS{
		files: map[string][]byte{
			filePath: []byte(initialCode),
		},
	}

	handler := commands.NewExecutePlanHandler(fs)

	req := domain.ImplementRequest{
		Interface: "context.Context",
		Receiver:  "*MyContext",
		FilePath:  filePath,
	}

	results, err := handler.Implement(context.Background(), req)
	require.NoError(t, err)

	// context.Context has 4 methods: Deadline, Done, Err, Value
	assert.Len(t, results, 4)

	// Check if file content was updated
	updatedContent := string(fs.files[filePath])
	assert.Contains(t, updatedContent, "func (r *MyContext) Deadline() (deadline time.Time, ok bool) {")
	assert.Contains(t, updatedContent, "func (r *MyContext) Done() <-chan struct{} {")
	assert.Contains(t, updatedContent, "func (r *MyContext) Err() error {")
	assert.Contains(t, updatedContent, "func (r *MyContext) Value(key any) any {")
	assert.Contains(t, updatedContent, "panic(\"not implemented\")")
	assert.Contains(t, updatedContent, "// TODO: implement Deadline")

	// Check SymbolResult details
	var foundDeadline bool
	for _, res := range results {
		if res.Name == "Deadline" {
			foundDeadline = true
			assert.Equal(t, "MyContext", res.Receiver)
			assert.Contains(t, res.Signature, "Deadline() (deadline time.Time, ok bool)")
			assert.Contains(t, res.Code, "panic(\"not implemented\")")
		}
	}
	assert.True(t, foundDeadline)

	// Test avoiding duplicates
	results2, err := handler.Implement(context.Background(), req)
	require.NoError(t, err)
	assert.Len(t, results2, 0, "Should return 0 new methods if already implemented")
}
