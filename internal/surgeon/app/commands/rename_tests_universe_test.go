package commands_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/outbound/filesystem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeRenameTestsModule scaffolds a module whose lib package has both an
// in-package and an external test file referencing lib.Helper. With
// Tests=true, packages.Load returns the symbol in two universes (lib and
// lib [lib.test]) — rename must still resolve it and rewrite test files.
func writeRenameTestsModule(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/renametests\n\ngo 1.25\n"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "lib"), 0755))

	libSrc := `package lib

func Helper() string {
	return "help"
}

func caller() string {
	return Helper()
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib", "lib.go"), []byte(libSrc), 0644))

	inPkgTestSrc := `package lib

import "testing"

func TestHelperInPackage(t *testing.T) {
	if Helper() == "" {
		t.Fatal("empty")
	}
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib", "lib_inpkg_test.go"), []byte(inPkgTestSrc), 0644))

	extTestSrc := `package lib_test

import (
	"testing"

	"example.com/renametests/lib"
)

func TestHelperExternal(t *testing.T) {
	if lib.Helper() == "" {
		t.Fatal("empty")
	}
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib", "lib_test.go"), []byte(extTestSrc), 0644))
}

// TestRename_TestsTrue_RewritesTestFileReferences pins the Tests=true
// contract: a uniquely-declared symbol must resolve (no "ambiguous"
// error from the pkg vs pkg.test universes) and references inside both
// in-package and external _test.go files must be rewritten.
func TestRename_TestsTrue_RewritesTestFileReferences(t *testing.T) {
	dir := t.TempDir()
	writeRenameTestsModule(t, dir)

	handler := commands.NewExecutePlanHandler(filesystem.NewFileSystem())

	runInDir(t, dir, func() {
		result, err := handler.Rename(context.Background(), domain.RenameRequest{
			Symbol:  domain.SymbolRef{Name: "Helper"},
			NewName: "Assist",
			Dir:     ".",
			Tests:   true,
		})
		require.NoError(t, err, "a uniquely-declared symbol must not be ambiguous with tests=true")
		assert.Equal(t, "func", result.Kind)

		libBytes, err := os.ReadFile(filepath.Join(dir, "lib", "lib.go"))
		require.NoError(t, err)
		assert.Contains(t, string(libBytes), "func Assist()")
		assert.Contains(t, string(libBytes), "return Assist()")
		assert.NotContains(t, string(libBytes), "Helper")

		inPkgBytes, err := os.ReadFile(filepath.Join(dir, "lib", "lib_inpkg_test.go"))
		require.NoError(t, err)
		assert.Contains(t, string(inPkgBytes), "Assist()", "in-package test reference must be rewritten")
		assert.NotContains(t, string(inPkgBytes), "Helper()")

		extBytes, err := os.ReadFile(filepath.Join(dir, "lib", "lib_test.go"))
		require.NoError(t, err)
		assert.Contains(t, string(extBytes), "lib.Assist()", "external test-package reference must be rewritten")
		assert.NotContains(t, string(extBytes), "lib.Helper()")
	})
}

// TestRename_TestsFalse_DoesNotCorruptSources documents the Tests=false
// behavior in a package that has test files: the rename must still
// succeed and rewrite non-test files exactly; _test.go files are not
// loaded, so they stay byte-identical (never partially rewritten).
func TestRename_TestsFalse_DoesNotCorruptSources(t *testing.T) {
	dir := t.TempDir()
	writeRenameTestsModule(t, dir)

	inPkgBefore, err := os.ReadFile(filepath.Join(dir, "lib", "lib_inpkg_test.go"))
	require.NoError(t, err)
	extBefore, err := os.ReadFile(filepath.Join(dir, "lib", "lib_test.go"))
	require.NoError(t, err)

	handler := commands.NewExecutePlanHandler(filesystem.NewFileSystem())

	runInDir(t, dir, func() {
		result, err := handler.Rename(context.Background(), domain.RenameRequest{
			Symbol:  domain.SymbolRef{Name: "Helper"},
			NewName: "Assist",
			Dir:     ".",
			Tests:   false,
		})
		require.NoError(t, err)
		assert.Equal(t, "func", result.Kind)

		libBytes, err := os.ReadFile(filepath.Join(dir, "lib", "lib.go"))
		require.NoError(t, err)
		assert.Contains(t, string(libBytes), "func Assist()")
		assert.NotContains(t, string(libBytes), "Helper")

		inPkgAfter, err := os.ReadFile(filepath.Join(dir, "lib", "lib_inpkg_test.go"))
		require.NoError(t, err)
		assert.Equal(t, string(inPkgBefore), string(inPkgAfter), "unloaded test files must stay byte-identical")
		extAfter, err := os.ReadFile(filepath.Join(dir, "lib", "lib_test.go"))
		require.NoError(t, err)
		assert.Equal(t, string(extBefore), string(extAfter), "unloaded test files must stay byte-identical")
	})
}
