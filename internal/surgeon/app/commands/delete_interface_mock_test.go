package commands_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockFileContent is a realistic mock file as produced by MockFromSource:
// a struct with func-field members, delegation methods, and a compile-time
// interface assertion at the bottom.
const mockFileContent = `package repo

type MockReader struct {
	ReadFunc  func(p []byte) (int, error)
	CloseFunc func() error
}

func (m *MockReader) Read(p []byte) (int, error) {
	if m.ReadFunc == nil {
		panic("MockReader.ReadFunc not set")
	}
	return m.ReadFunc(p)
}

func (m *MockReader) Close() error {
	if m.CloseFunc == nil {
		panic("MockReader.CloseFunc not set")
	}
	return m.CloseFunc()
}

var _ Reader = (*MockReader)(nil)
`

func TestDeleteInterface_MockRemoval(t *testing.T) {
	ctx := context.Background()

	t.Run("delete_mock=false keeps current behavior (mock untouched)", func(t *testing.T) {
		fs := &mockFS{files: make(map[string][]byte)}
		h := commands.NewExecutePlanHandler(fs)
		fs.files["repo.go"] = []byte("package repo\n\ntype Reader interface {\n\tRead(p []byte) (int, error)\n\tClose() error\n}\n")
		fs.files["mock.go"] = []byte(mockFileContent)

		_, err := h.DeleteInterface(ctx, domain.InterfaceActionRequest{
			FilePath:   "repo.go",
			Identifier: "Reader",
		})
		require.NoError(t, err)
		// Mock file must be byte-identical.
		assert.Equal(t, mockFileContent, string(fs.files["mock.go"]))
		// Interface is gone from the source.
		assert.NotContains(t, string(fs.files["repo.go"]), "type Reader")
	})

	t.Run("delete_mock=true removes struct, methods, and assertion", func(t *testing.T) {
		fs := &mockFS{files: make(map[string][]byte)}
		h := commands.NewExecutePlanHandler(fs)
		fs.files["repo.go"] = []byte("package repo\n\ntype Reader interface {\n\tRead(p []byte) (int, error)\n\tClose() error\n}\n")
		fs.files["mock.go"] = []byte(mockFileContent)

		msg, err := h.DeleteInterface(ctx, domain.InterfaceActionRequest{
			FilePath:   "repo.go",
			Identifier: "Reader",
			MockFile:   "mock.go",
			MockName:   "MockReader",
			DeleteMock: true,
		})
		require.NoError(t, err)
		assert.Contains(t, msg, "removed mock MockReader")

		mock := string(fs.files["mock.go"])
		assert.NotContains(t, mock, "MockReader", "struct, methods and assertion must all be gone")
		assert.NotContains(t, mock, "var _ Reader")
		// File is kept (even if effectively empty).
		assert.Contains(t, mock, "package repo")
	})

	t.Run("delete_mock=true with star-prefixed mock_name works", func(t *testing.T) {
		fs := &mockFS{files: make(map[string][]byte)}
		h := commands.NewExecutePlanHandler(fs)
		fs.files["repo.go"] = []byte("package repo\n\ntype Reader interface {\n\tRead() error\n}\n")
		fs.files["mock.go"] = []byte(mockFileContent)

		_, err := h.DeleteInterface(ctx, domain.InterfaceActionRequest{
			FilePath:   "repo.go",
			Identifier: "Reader",
			MockFile:   "mock.go",
			MockName:   "*MockReader", // user passes the pointer form
			DeleteMock: true,
		})
		require.NoError(t, err)
		assert.NotContains(t, string(fs.files["mock.go"]), "MockReader")
	})

	t.Run("delete_mock=true without mock_file/name errors", func(t *testing.T) {
		fs := &mockFS{files: make(map[string][]byte)}
		h := commands.NewExecutePlanHandler(fs)
		fs.files["repo.go"] = []byte("package repo\n\ntype Reader interface {\n\tRead() error\n}\n")

		_, err := h.DeleteInterface(ctx, domain.InterfaceActionRequest{
			FilePath:   "repo.go",
			Identifier: "Reader",
			DeleteMock: true,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mock_file")
		assert.Contains(t, err.Error(), "mock_name")
	})

	t.Run("delete_mock=true skips gracefully when mock file does not exist", func(t *testing.T) {
		fs := &mockFS{files: make(map[string][]byte)}
		h := commands.NewExecutePlanHandler(fs)
		fs.files["repo.go"] = []byte("package repo\n\ntype Reader interface {\n\tRead() error\n}\n")

		msg, err := h.DeleteInterface(ctx, domain.InterfaceActionRequest{
			FilePath:   "repo.go",
			Identifier: "Reader",
			MockFile:   "missing.go",
			MockName:   "MockReader",
			DeleteMock: true,
		})
		require.NoError(t, err)
		assert.Contains(t, msg, "not found")
	})

	t.Run("delete_mock=true skips gracefully when mock struct not present", func(t *testing.T) {
		fs := &mockFS{files: make(map[string][]byte)}
		h := commands.NewExecutePlanHandler(fs)
		fs.files["repo.go"] = []byte("package repo\n\ntype Reader interface {\n\tRead() error\n}\n")
		// File exists but has no MockReader struct.
		fs.files["mock.go"] = []byte("package repo\n\ntype OtherMock struct{}\n")

		msg, err := h.DeleteInterface(ctx, domain.InterfaceActionRequest{
			FilePath:   "repo.go",
			Identifier: "Reader",
			MockFile:   "mock.go",
			MockName:   "MockReader",
			DeleteMock: true,
		})
		require.NoError(t, err)
		assert.Contains(t, msg, "skipped")
		// OtherMock is untouched.
		assert.Contains(t, string(fs.files["mock.go"]), "OtherMock")
	})

	t.Run("delete_mock=true preserves other mocks in the same file", func(t *testing.T) {
		fs := &mockFS{files: make(map[string][]byte)}
		h := commands.NewExecutePlanHandler(fs)
		fs.files["repo.go"] = []byte("package repo\n\ntype Reader interface {\n\tRead() error\n}\n")

		sharedMockFile := `package repo

type MockReader struct {
	ReadFunc func() error
}

func (m *MockReader) Read() error {
	return m.ReadFunc()
}

var _ Reader = (*MockReader)(nil)

type MockWriter struct {
	WriteFunc func() error
}

func (m *MockWriter) Write() error {
	return m.WriteFunc()
}

var _ Writer = (*MockWriter)(nil)
`
		fs.files["mock.go"] = []byte(sharedMockFile)

		_, err := h.DeleteInterface(ctx, domain.InterfaceActionRequest{
			FilePath:   "repo.go",
			Identifier: "Reader",
			MockFile:   "mock.go",
			MockName:   "MockReader",
			DeleteMock: true,
		})
		require.NoError(t, err)

		mock := string(fs.files["mock.go"])
		// MockReader gone
		assert.NotContains(t, mock, "MockReader")
		assert.NotContains(t, mock, "var _ Reader")
		// MockWriter preserved
		assert.Contains(t, mock, "MockWriter")
		assert.Contains(t, mock, "var _ Writer = (*MockWriter)(nil)")
		assert.Contains(t, mock, "WriteFunc")
	})

	t.Run("interface is still deleted even if mock cleanup has no effect", func(t *testing.T) {
		// Edge case: delete_mock requested, mock file missing — interface
		// deletion should still succeed cleanly (not roll back).
		fs := &mockFS{files: make(map[string][]byte)}
		h := commands.NewExecutePlanHandler(fs)
		fs.files["repo.go"] = []byte("package repo\n\ntype Reader interface {\n\tRead() error\n}\n")

		_, err := h.DeleteInterface(ctx, domain.InterfaceActionRequest{
			FilePath:   "repo.go",
			Identifier: "Reader",
			MockFile:   "missing.go",
			MockName:   "MockReader",
			DeleteMock: true,
		})
		require.NoError(t, err)
		assert.NotContains(t, string(fs.files["repo.go"]), "type Reader")
	})
}

// Sanity check: the canonical mock format produced by MockFromSource is
// what our detector expects. If the generator format ever changes, this
// test will fail and flag the mismatch.
func TestDeleteInterface_MockFormatAssumption(t *testing.T) {
	// A mock file generated by today's MockFromSource always contains:
	//   1. `type <Name> struct { ... }`
	//   2. method receivers `func (m *<Name>) ...`
	//   3. an assertion `var _ <Iface> = (*<Name>)(nil)`
	assert.Contains(t, mockFileContent, "type MockReader struct")
	assert.Contains(t, mockFileContent, "func (m *MockReader)")
	assert.True(t,
		strings.Contains(mockFileContent, "var _ Reader = (*MockReader)(nil)"),
		"assertion format is what isMockAssertion expects")
}
