package filesystem

import (
	"context"
)

// FileSystem defines the interface for file system operations.
//
// WriteFile applies goimports to .go paths as part of the write and returns
// the list of import paths it added (net-new compared to the previous file
// contents). Callers should surface these to the agent so it can see which
// imports were auto-resolved.
type FileSystem interface {
	ReadFile(ctx context.Context, path string) ([]byte, error)
	WriteFile(ctx context.Context, path string, data []byte) (addedImports []string, err error)
	ReadDir(ctx context.Context, path string) ([]string, error)
	IsDir(ctx context.Context, path string) (bool, error)
	MkdirAll(ctx context.Context, path string) error
}
