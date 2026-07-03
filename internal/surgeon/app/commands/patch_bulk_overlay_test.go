package commands_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/imports"
)

// goimportsFS is a test filesystem that mirrors the real outbound adapter:
// every .go write is run through goimports before being stored, so line
// numbers shift when an import is added. It exists to reproduce the bulk
// phase-2 divergence, where a same-file at_line item resolved cleanly
// against the un-goimports'd overlay but off-by-N against the transformed
// on-disk content of an earlier item.
type goimportsFS struct {
	files map[string][]byte
}

func newGoimportsFS() *goimportsFS {
	return &goimportsFS{files: make(map[string][]byte)}
}

func (m *goimportsFS) ReadFile(_ context.Context, path string) ([]byte, error) {
	if content, ok := m.files[path]; ok {
		return content, nil
	}
	return nil, os.ErrNotExist
}

func (m *goimportsFS) WriteFile(_ context.Context, path string, content []byte) ([]string, error) {
	if strings.HasSuffix(path, ".go") {
		if formatted, err := imports.Process(path, content, nil); err == nil {
			content = formatted
		}
	}
	m.files[path] = content
	return nil, nil
}

func (m *goimportsFS) ReadDir(_ context.Context, path string) ([]string, error) {
	var names []string
	for k := range m.files {
		if filepath.Dir(k) == path {
			names = append(names, filepath.Base(k))
		}
	}
	return names, nil
}

func (m *goimportsFS) IsDir(_ context.Context, _ string) (bool, error) { return false, nil }
func (m *goimportsFS) MkdirAll(_ context.Context, _ string) error      { return nil }
func (m *goimportsFS) DeleteFile(_ context.Context, _ string) error    { return nil }

// TestPatchFunctionBulk_SameFileAtLineSurvivesGoimportsShift proves item 7:
// item #1 introduces an fmt reference, so a real write runs goimports and
// inserts `import "fmt"`, pushing the body of Bravo down two lines. Item #2
// targets Bravo by file-absolute at_line. Under the old phase-2 re-run the
// at_line was resolved against the already-shifted on-disk file and fell out
// of Bravo's body, so the whole call errored after item #1 was already
// written to disk. With a single overlay + commit, both items resolve
// against consistent content and goimports runs once at commit.
func TestPatchFunctionBulk_SameFileAtLineSurvivesGoimportsShift(t *testing.T) {
	ctx := context.Background()
	fs := newGoimportsFS()
	h := commands.NewExecutePlanHandler(fs)

	fs.files["a.go"] = []byte(`package p

func Alpha() {
	x := 1
	_ = x
}

func Bravo() {
	a := 10
	b := 20
	_ = a
	_ = b
}
`)

	res, err := h.PatchFunctionBulk(ctx, domain.PatchFunctionBulkRequest{
		Items: []domain.PatchFunctionBulkItem{
			{FilePath: "a.go", Identifier: "Alpha", Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "_ = x", Replace: "fmt.Println(x)"},
			}},
			// at_line 9 (the `a := 10` line) is only correct against the
			// pre-goimports layout; a real write of item #1 shifts it down.
			{FilePath: "a.go", Identifier: "Bravo", Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, AtLine: 9, Replace: "a := 999"},
			}},
		},
	})

	require.NoError(t, err)
	assert.Equal(t, 2, res.Applied)

	out := string(fs.files["a.go"])
	assert.Contains(t, out, "fmt.Println(x)")
	assert.Contains(t, out, "a := 999", "item #2 must hit the line it resolved against, not a shifted one")
	assert.Contains(t, out, "b := 20")
	assert.NotContains(t, out, "a := 10")
	assert.Contains(t, out, `"fmt"`, "goimports should still run once at commit")
}
