package filesystem

import (
	"context"
)

// FileSystem defines the interface for file system operations.
type FileSystem interface {
	ReadFile(ctx context.Context, path string) ([]byte, error)
	WriteFile(ctx context.Context, path string, data []byte) error
	ReadDir(ctx context.Context, path string) ([]string, error)
	IsDir(ctx context.Context, path string) (bool, error)
	MkdirAll(ctx context.Context, path string) error
	ExecuteGoImports(ctx context.Context, files []string) error
}
