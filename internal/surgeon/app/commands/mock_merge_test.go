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

// TestMock_SecondMockPreservesFirst is a regression test for issue #17: the
// types-based Mock rebuilt the whole target file, so mocking a second
// interface into the same file destroyed the first mock. MockFromSource
// merges surgically; Mock must too.
func TestMock_SecondMockPreservesFirst(t *testing.T) {
	tmpDir := t.TempDir()
	dirPath := filepath.Join(tmpDir, "mocks")
	filePath := filepath.Join(dirPath, "mocks.go")

	fs := &mockFS{files: map[string][]byte{}}
	handler := commands.NewExecutePlanHandler(fs)

	_, err := handler.Mock(context.Background(), domain.MockRequest{
		Interface: "context.Context",
		Receiver:  "MockContext",
		FilePath:  filePath,
	})
	require.NoError(t, err)

	_, err = handler.Mock(context.Background(), domain.MockRequest{
		Interface: "io.Reader",
		Receiver:  "MockReader",
		FilePath:  filePath,
	})
	require.NoError(t, err)

	content := string(fs.files[filePath])

	// Both mocks must coexist in the same file.
	assert.Contains(t, content, "type MockContext struct {", "first mock must survive a second mock into the same file")
	assert.Contains(t, content, "type MockReader struct {", "second mock must be present")
	assert.Contains(t, content, "func (m *MockContext) Deadline()", "first mock's methods must survive")
	assert.Contains(t, content, "func (m *MockReader) Read(", "second mock's methods must be present")
	assert.Contains(t, content, "var _ context.Context = (*MockContext)(nil)", "first mock's assertion must survive")
	assert.Contains(t, content, "var _ io.Reader = (*MockReader)(nil)", "second mock's assertion must be present")
}
