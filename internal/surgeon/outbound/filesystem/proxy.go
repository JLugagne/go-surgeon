package filesystem

import (
	"context"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/repositories/filesystem"
)

// ProxyFileSystem allows swapping the underlying file system implementation dynamically.
type ProxyFileSystem struct {
	Active filesystem.FileSystem

	// OnWrite, when set, fires after every successful write or delete —
	// Setup uses it to invalidate the shared packages-loader cache.
	OnWrite func()
}

func (p *ProxyFileSystem) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return p.Active.ReadFile(ctx, path)
}

func (p *ProxyFileSystem) WriteFile(ctx context.Context, path string, data []byte) ([]string, error) {
	res, err := p.Active.WriteFile(ctx, path, data)
	if err == nil && p.OnWrite != nil {
		p.OnWrite()
	}
	return res, err
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

func (p *ProxyFileSystem) DeleteFile(ctx context.Context, path string) error {
	err := p.Active.DeleteFile(ctx, path)
	if err == nil && p.OnWrite != nil {
		p.OnWrite()
	}
	return err
}
