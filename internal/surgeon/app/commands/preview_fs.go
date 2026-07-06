package commands

import (
	"context"
	"os"
	"sort"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/repositories/filesystem"
	"github.com/pmezard/go-difflib/difflib"
)

// previewFS is a commands-local dry-run filesystem. It accumulates writes
// in memory and can emit a unified diff of every would-be change. It
// mirrors the behavior of outbound/filesystem.DryRunFileSystem but lives
// here to avoid an upward import from the app layer to the outbound
// layer. The two implementations are intentionally similar.
type previewFS struct {
	real    filesystem.FileSystem
	files   map[string][]byte
	wrote   map[string]bool
	deleted map[string]bool
}

func newPreviewFS(real filesystem.FileSystem) *previewFS {
	return &previewFS{
		real:    real,
		files:   make(map[string][]byte),
		wrote:   make(map[string]bool),
		deleted: make(map[string]bool),
	}
}

func (f *previewFS) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if f.deleted[path] {
		return nil, os.ErrNotExist
	}
	if data, ok := f.files[path]; ok {
		return data, nil
	}
	return f.real.ReadFile(ctx, path)
}

// WriteFile records the intended write instead of touching disk. It
// returns no added imports because the preview path never runs goimports
// — we want the diff to reflect exactly what callers typed. Tools that
// rely on import auto-fixing will still see that happen when Preview is
// false (the normal path).
// WriteFile records the intended write instead of touching disk. It
// returns no added imports because the preview path never runs goimports
// — we want the diff to reflect exactly what callers typed. Tools that
// rely on import auto-fixing will still see that happen when Preview is
// false (the normal path). A write resurrects a previously-deleted path.
func (f *previewFS) WriteFile(ctx context.Context, path string, content []byte) ([]string, error) {
	f.files[path] = content
	f.wrote[path] = true
	delete(f.deleted, path)
	return nil, nil
}

func (f *previewFS) ReadDir(ctx context.Context, path string) ([]string, error) {
	return f.real.ReadDir(ctx, path)
}

func (f *previewFS) IsDir(ctx context.Context, path string) (bool, error) {
	return f.real.IsDir(ctx, path)
}

// MkdirAll is a no-op on a preview filesystem: we never want to create
// directories on disk during a dry-run.
func (f *previewFS) MkdirAll(ctx context.Context, path string) error {
	return nil
}

// DeleteFile records the deletion so reads see the file as gone and Diff
// renders the removal.
func (f *previewFS) DeleteFile(ctx context.Context, path string) error {
	f.deleted[path] = true
	delete(f.files, path)
	delete(f.wrote, path)
	return nil
}

// Diff returns a deterministic unified diff of every pending write.
// Files are sorted by path so repeated calls produce the same output.
// Diff returns a deterministic unified diff of every pending write and
// deletion. Files are sorted by path so repeated calls produce the same
// output.
func (f *previewFS) Diff(ctx context.Context) (string, error) {
	paths := make([]string, 0, len(f.files)+len(f.deleted))
	for p := range f.files {
		paths = append(paths, p)
	}
	for p := range f.deleted {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var out strings.Builder
	for _, path := range paths {
		var original string
		if origBytes, err := f.real.ReadFile(ctx, path); err == nil {
			original = string(origBytes)
		}
		var updated string
		if !f.deleted[path] {
			updated = string(f.files[path])
		}
		diff := difflib.UnifiedDiff{
			A:        difflib.SplitLines(original),
			B:        difflib.SplitLines(updated),
			FromFile: path,
			ToFile:   path,
			Context:  3,
		}
		text, err := difflib.GetUnifiedDiffString(diff)
		if err != nil {
			return "", err
		}
		out.WriteString(text)
	}
	return out.String(), nil
}

// WrittenFiles returns the list of file paths that received a write on
// this preview FS, sorted for deterministic ordering.
// WrittenFiles returns every path this preview FS would change on a real
// run — writes and deletions — sorted for deterministic ordering.
func (f *previewFS) WrittenFiles() []string {
	paths := make([]string, 0, len(f.wrote)+len(f.deleted))
	for p := range f.wrote {
		paths = append(paths, p)
	}
	for p := range f.deleted {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}
