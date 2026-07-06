package commands_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/outbound/filesystem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// TestGenerateTest_SamePackageParamResultTypesQualified is a regression test
// for backlog item 19: in a black-box (`pkg_test`) skeleton, same-package named
// types used as param/result types must be package-qualified, otherwise the
// generated file does not compile (undefined identifiers + unused import).
func TestGenerateTest_SamePackageParamResultTypesQualified(t *testing.T) {
	tmpDir := t.TempDir()

	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "go.mod"),
		[]byte("module example.com/myapp\n\ngo 1.21\n"),
		0o644,
	))

	srcDir := filepath.Join(tmpDir, "app")
	require.NoError(t, os.MkdirAll(srcDir, 0o755))
	srcPath := filepath.Join(srcDir, "service.go")
	require.NoError(t, os.WriteFile(srcPath, []byte(`package app

type Request struct{ Name string }

type Response struct{ ID int }

func Handle(req Request, tags []Request) (Response, error) {
	return Response{}, nil
}
`), 0o644))

	fs := filesystem.NewFileSystem()
	handler := commands.NewExecutePlanHandler(fs)

	testFile, err := handler.GenerateTest(context.Background(), srcPath, "Handle")
	require.NoError(t, err)

	got, err := os.ReadFile(testFile)
	require.NoError(t, err)
	content := string(got)

	// Black-box test file: same-package named types must be qualified.
	assert.Contains(t, content, "package app_test")
	assert.Contains(t, content, "app.Request", "param type must be package-qualified")
	assert.Contains(t, content, "[]app.Request", "slice-of-same-package type must be qualified")
	assert.Contains(t, content, "app.Response", "result type must be package-qualified")
	assert.NotContains(t, content, "req Request", "bare same-package type must not appear")

	// The generated skeleton must type-check as part of the module.
	errs := typeCheckErrors(t, tmpDir)
	assert.Empty(t, errs, "generated black-box skeleton must compile; got: %s", strings.Join(errs, "\n"))
}

// typeCheckErrors loads every package (including test variants) rooted at dir
// and returns all reported package/type errors as strings.
func typeCheckErrors(t *testing.T, dir string) []string {
	t.Helper()
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedTypes | packages.NeedSyntax |
			packages.NeedTypesInfo | packages.NeedDeps,
		Dir:   dir,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, "./...")
	require.NoError(t, err)
	var out []string
	for _, p := range pkgs {
		for _, e := range p.Errors {
			out = append(out, e.Error())
		}
	}
	return out
}
