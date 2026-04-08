package commands_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMock(t *testing.T) {
	tmpDir := t.TempDir()
	dirPath := filepath.Join(tmpDir, "mocktest")
	existingFile := filepath.Join(dirPath, "existing.go")
	filePath := filepath.Join(dirPath, "mock_context.go")

	fs := &mockFS{
		files: map[string][]byte{
			existingFile: []byte("package mocktest\n"),
		},
	}

	handler := commands.NewExecutePlanHandler(fs)

	req := domain.MockRequest{
		Interface: "context.Context",
		Receiver:  "MockContext",
		FilePath:  filePath,
	}

	result, err := handler.Mock(context.Background(), req)
	require.NoError(t, err)
	assert.Contains(t, result, "MockContext")
	assert.Contains(t, result, "4 methods")

	content := string(fs.files[filePath])

	// Package declaration
	assert.Contains(t, content, "package mocktest")

	// Struct with function fields
	assert.Contains(t, content, "type MockContext struct {")
	assert.Contains(t, content, "DeadlineFunc func() (deadline time.Time, ok bool)")
	assert.Contains(t, content, "DoneFunc func() <-chan struct{}")
	assert.Contains(t, content, "ErrFunc func() error")
	assert.Contains(t, content, "ValueFunc func(key any) any")

	// Delegation methods with pointer receiver
	assert.Contains(t, content, "func (m *MockContext) Deadline() (deadline time.Time, ok bool)")
	assert.Contains(t, content, "func (m *MockContext) Done() <-chan struct{}")
	assert.Contains(t, content, "func (m *MockContext) Err() error")
	assert.Contains(t, content, "func (m *MockContext) Value(key any) any")

	// Nil panics
	assert.Contains(t, content, `panic("MockContext.DeadlineFunc not set")`)
	assert.Contains(t, content, `panic("MockContext.DoneFunc not set")`)
	assert.Contains(t, content, `panic("MockContext.ErrFunc not set")`)
	assert.Contains(t, content, `panic("MockContext.ValueFunc not set")`)

	// Delegation calls
	assert.Contains(t, content, "return m.DeadlineFunc()")
	assert.Contains(t, content, "return m.DoneFunc()")
	assert.Contains(t, content, "return m.ErrFunc()")
	assert.Contains(t, content, "return m.ValueFunc(key)")

	// Compile-time check
	assert.Contains(t, content, "var _ context.Context = (*MockContext)(nil)")
}

func TestMock_StarReceiver(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "mock.go")

	fs := &mockFS{files: map[string][]byte{}}
	handler := commands.NewExecutePlanHandler(fs)

	req := domain.MockRequest{
		Interface: "context.Context",
		Receiver:  "*MockContext",
		FilePath:  filePath,
	}

	result, err := handler.Mock(context.Background(), req)
	require.NoError(t, err)
	assert.Contains(t, result, "MockContext")

	content := string(fs.files[filePath])
	// Star should be stripped for type name, but used in methods
	assert.Contains(t, content, "type MockContext struct {")
	assert.Contains(t, content, "func (m *MockContext) Deadline()")
}

func TestMock_PackageFallback(t *testing.T) {
	tmpDir := t.TempDir()
	dirPath := filepath.Join(tmpDir, "mypkg")
	filePath := filepath.Join(dirPath, "mock.go")

	// No existing .go files — should fall back to directory name
	fs := &mockFS{files: map[string][]byte{}}
	handler := commands.NewExecutePlanHandler(fs)

	req := domain.MockRequest{
		Interface: "context.Context",
		Receiver:  "MockContext",
		FilePath:  filePath,
	}

	_, err := handler.Mock(context.Background(), req)
	require.NoError(t, err)

	content := string(fs.files[filePath])
	assert.Contains(t, content, "package mypkg")
}

func TestMock_MissingFields(t *testing.T) {
	fs := &mockFS{files: map[string][]byte{}}
	handler := commands.NewExecutePlanHandler(fs)

	_, err := handler.Mock(context.Background(), domain.MockRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestMock_InvalidInterface(t *testing.T) {
	fs := &mockFS{files: map[string][]byte{}}
	handler := commands.NewExecutePlanHandler(fs)

	_, err := handler.Mock(context.Background(), domain.MockRequest{
		Interface: "nonexistent.Iface",
		Receiver:  "Mock",
		FilePath:  "/tmp/mock.go",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to resolve interface")
}
