package filesystem

import (
	"context"
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/repositories/filesystem"
	"github.com/pmezard/go-difflib/difflib"
	"golang.org/x/tools/imports"
)

// DryRunFileSystem accumulates file changes in memory and prints unified diffs on Close.
type DryRunFileSystem struct {
	real  filesystem.FileSystem
	files map[string][]byte
}

func NewDryRunFileSystem(real filesystem.FileSystem) *DryRunFileSystem {
	return &DryRunFileSystem{
		real:  real,
		files: make(map[string][]byte),
	}
}

func (f *DryRunFileSystem) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if data, ok := f.files[path]; ok {
		return data, nil
	}
	return f.real.ReadFile(ctx, path)
}

func (f *DryRunFileSystem) WriteFile(ctx context.Context, path string, content []byte) error {
	if strings.HasSuffix(path, ".go") {
		formatted, err := imports.Process(path, content, nil)
		if err == nil {
			content = formatted
		}
	}
	f.files[path] = content
	return nil
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

func (f *DryRunFileSystem) ExecuteGoImports(ctx context.Context, files []string) error {
	return nil
}

// PrintDiffs prints all accumulated diffs to stdout.
func (f *DryRunFileSystem) PrintDiffs(ctx context.Context) error {
	for path, content := range f.files {
		var original string
		origBytes, err := f.real.ReadFile(ctx, path)
		if err == nil {
			original = string(origBytes)
		}

		diff := difflib.UnifiedDiff{
			A:        difflib.SplitLines(original),
			B:        difflib.SplitLines(string(content)),
			FromFile: path,
			ToFile:   path,
			Context:  3,
		}
		text, err := difflib.GetUnifiedDiffString(diff)
		if err != nil {
			return err
		}
		if text != "" {
			fmt.Print(text)
		}
	}
	return nil
}
