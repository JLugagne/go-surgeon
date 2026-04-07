package filesystem

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/tools/imports"
)

// FileSystem is an adapter that interacts with the real file system.
type FileSystem struct{}

// NewFileSystem creates a new FileSystem adapter.
func NewFileSystem() *FileSystem {
	return &FileSystem{}
}

// ReadFile reads the content of the file at path.
func (f *FileSystem) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}

// WriteFile writes content to the file at path.
func (f *FileSystem) WriteFile(ctx context.Context, path string, content []byte) error {
	if strings.HasSuffix(path, ".go") {
		formatted, err := imports.Process(path, content, nil)
		if err == nil {
			content = formatted
		}
	}
	return os.WriteFile(path, content, 0644)
}

// ReadDir returns the names of the files and directories in path.
func (f *FileSystem) ReadDir(ctx context.Context, path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names, nil
}

// IsDir returns true if the path is a directory.
func (f *FileSystem) IsDir(ctx context.Context, path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

// MkdirAll creates a directory and all necessary parents.
func (f *FileSystem) MkdirAll(ctx context.Context, path string) error {
	return os.MkdirAll(path, 0755)
}

// ExecuteGoImports executes goimports -w on the provided files.
func (f *FileSystem) ExecuteGoImports(ctx context.Context, files []string) error {
	if len(files) == 0 {
		return nil
	}

	args := append([]string{"-w"}, files...)
	cmd := exec.CommandContext(ctx, "goimports", args...)
	return cmd.Run()
}
