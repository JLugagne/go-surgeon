package filesystem

import (
	"context"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/repositories/filesystem"
)

// ProxyFileSystem allows swapping the underlying file system implementation dynamically.
type ProxyFileSystem struct {
	Active filesystem.FileSystem
}

func (p *ProxyFileSystem) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return p.Active.ReadFile(ctx, path)
}

func (p *ProxyFileSystem) WriteFile(ctx context.Context, path string, data []byte) error {
	return p.Active.WriteFile(ctx, path, data)
}

func (p *ProxyFileSystem) ReadDir(ctx context.Context, path string) ([]string, error) {
	return p.Active.ReadDir(ctx, path)
}

func (p *ProxyFileSystem) IsDir(ctx context.Context, path string) (bool, error) {
	return p.Active.IsDir(ctx, path)
}

func (p *ProxyFileSystem) MkdirAll(ctx context.Context, path string) error {
	return p.Active.MkdirAll(ctx, path)
}

func (p *ProxyFileSystem) ExecuteGoImports(ctx context.Context, files []string) error {
	return p.Active.ExecuteGoImports(ctx, files)
}
