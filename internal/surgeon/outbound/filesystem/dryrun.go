package filesystem

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/repositories/filesystem"
	"github.com/pmezard/go-difflib/difflib"
)

// DryRunFileSystem accumulates file changes in memory and prints unified diffs on Close.
type DryRunFileSystem struct {
	real    filesystem.FileSystem
	files   map[string][]byte
	deleted map[string]bool
}

func NewDryRunFileSystem(real filesystem.FileSystem) *DryRunFileSystem {
	return &DryRunFileSystem{
		real:    real,
		files:   make(map[string][]byte),
		deleted: make(map[string]bool),
	}
}

func (f *DryRunFileSystem) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if f.deleted[path] {
		return nil, os.ErrNotExist
	}
	if data, ok := f.files[path]; ok {
		return data, nil
	}
	return f.real.ReadFile(ctx, path)
}

// WriteFile records the formatted content in memory. A write resurrects a
// previously-deleted path (mirrors delete-then-recreate plans).
func (f *DryRunFileSystem) WriteFile(ctx context.Context, path string, content []byte) ([]string, error) {
	content, addedImports := applyGoImports(path, content)
	f.files[path] = content
	delete(f.deleted, path)
	return addedImports, nil
}

func (f *DryRunFileSystem) ReadDir(ctx context.Context, path string) ([]string, error) {
	return f.real.ReadDir(ctx, path)
}

func (f *DryRunFileSystem) IsDir(ctx context.Context, path string) (bool, error) {
	return f.real.IsDir(ctx, path)
}

func (f *DryRunFileSystem) MkdirAll(ctx context.Context, path string) error {
	return nil
}

// DeleteFile records the deletion so reads see the file as gone and
// CollectDiffs renders the removal.
func (f *DryRunFileSystem) DeleteFile(ctx context.Context, path string) error {
	if f.deleted == nil {
		f.deleted = make(map[string]bool)
	}
	f.deleted[path] = true
	delete(f.files, path)
	return nil
}

// PrintDiffs prints all accumulated diffs to stdout.
func (f *DryRunFileSystem) PrintDiffs(ctx context.Context) error {
	text, err := f.CollectDiffs(ctx)
	if err != nil {
		return err
	}
	if text != "" {
		fmt.Print(text)
	}
	return nil
}

// CollectDiffs returns a unified diff of every pending write accumulated on
// this filesystem. Deterministic output: files are sorted by path so the
// same sequence of edits always produces the same diff string (handy for
// tests and MCP preview responses).
// CollectDiffs returns a unified diff of every pending write and deletion
// accumulated on this filesystem. Deterministic output: files are sorted by
// path so the same sequence of edits always produces the same diff string
// (handy for tests and MCP preview responses).
func (f *DryRunFileSystem) CollectDiffs(ctx context.Context) (string, error) {
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
		origBytes, err := f.real.ReadFile(ctx, path)
		if err == nil {
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

// WrittenFiles returns the list of file paths that received a write on this
// dry-run filesystem, sorted for deterministic ordering.
// WrittenFiles returns every path that would change on a real run — writes
// and deletions — sorted for deterministic ordering.
func (f *DryRunFileSystem) WrittenFiles() []string {
	paths := make([]string, 0, len(f.files)+len(f.deleted))
	for p := range f.files {
		paths = append(paths, p)
	}
	for p := range f.deleted {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}
