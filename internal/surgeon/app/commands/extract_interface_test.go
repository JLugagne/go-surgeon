package commands_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractInterface(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "repo.go")
	mockFile := filepath.Join(tmpDir, "mock_repo.go")

	fs := &mockFS{files: make(map[string][]byte)}
	handler := commands.NewExecutePlanHandler(fs)
	ctx := context.Background()

	fs.files[filePath] = []byte(`package main

type MyRepo struct{}

func (r *MyRepo) Save(ctx context.Context, data string) error {
	return nil
}

func (r *MyRepo) Find(id int) (string, error) {
	return "", nil
}

func (r *MyRepo) internal() {}
`)

	t.Run("extract interface from struct", func(t *testing.T) {
		// Debug ReadDir
		dir := filepath.Dir(filePath)
		files, err := fs.ReadDir(ctx, dir)
		require.NoError(t, err)
		assert.Contains(t, files, "repo.go")

		req := domain.ExtractInterfaceRequest{
			FilePath:      filePath,
			StructName:    "MyRepo",
			InterfaceName: "Repository",
			MockFile:      mockFile,
			MockName:      "MockRepository",
		}

		_, err = handler.ExtractInterface(ctx, req)
		require.NoError(t, err)

		src := string(fs.files[filePath])
		assert.Contains(t, src, "type Repository interface {")
		assert.Contains(t, src, "Save(ctx context.Context, data string) error")
		assert.Contains(t, src, "Find(id int) (string, error)")

		// Check the interface block specifically
		ifaceStart := strings.Index(src, "type Repository interface")
		ifaceEnd := strings.Index(src[ifaceStart:], "}") + ifaceStart
		ifaceBlock := src[ifaceStart : ifaceEnd+1]
		assert.NotContains(t, ifaceBlock, "internal")

		mockSrc := string(fs.files[mockFile])
		assert.Contains(t, mockSrc, "type MockRepository struct {")
		assert.Contains(t, mockSrc, "SaveFunc func(ctx context.Context, data string) error")
	})
}
