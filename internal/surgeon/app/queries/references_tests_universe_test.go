package queries_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/queries"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTestsUniverseModule scaffolds a module whose lib package has both
// an in-package and an external test file referencing lib.Helper. With
// Tests=true, packages.Load returns the symbol in two universes (lib and
// lib [lib.test]) — resolution and reference collection must cope.
func writeTestsUniverseModule(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/reftests\n\ngo 1.25\n"), 0644))
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

	"example.com/reftests/lib"
)

func TestHelperExternal(t *testing.T) {
	if lib.Helper() == "" {
		t.Fatal("empty")
	}
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib", "lib_test.go"), []byte(extTestSrc), 0644))
}

// chdirInto chdirs into dir for the duration of the test so go/packages
// resolves relative to the temp module.
func chdirInto(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// TestFindReferences_TestsTrue_NotAmbiguousAndIncludesTestFileRefs pins
// the Tests=true contract: a symbol declared once must resolve without
// an "ambiguous" error even though it appears in both the plain and the
// test-augmented package universes, and references living in _test.go
// files must be reported.
func TestFindReferences_TestsTrue_NotAmbiguousAndIncludesTestFileRefs(t *testing.T) {
	dir := t.TempDir()
	writeTestsUniverseModule(t, dir)
	chdirInto(t, dir)

	h := queries.NewSurgeonQueriesHandler(nil)
	result, err := h.FindReferences(context.Background(), domain.ReferencesQuery{
		Symbol: domain.SymbolRef{Name: "Helper"},
		Dir:    ".",
		Tests:  true,
	})
	require.NoError(t, err, "a uniquely-declared symbol must not be ambiguous with tests=true")

	var files []string
	for _, ref := range result.References {
		files = append(files, filepath.Base(ref.File))
	}
	assert.Contains(t, files, "lib.go", "non-test reference must be reported; got %v", files)
	assert.Contains(t, files, "lib_inpkg_test.go", "in-package test reference must be reported; got %v", files)
	assert.Contains(t, files, "lib_test.go", "external test-package reference must be reported; got %v", files)

	// The same file loaded in two universes must not double-report.
	seen := map[string]int{}
	for _, ref := range result.References {
		seen[fmt.Sprintf("%s:%d:%d", ref.File, ref.Offset, ref.EndOffset)]++
	}
	for k, n := range seen {
		assert.Equal(t, 1, n, "reference %s reported %d times", k, n)
	}
}

// TestFindDefinition_TestsTrue_Resolves pins the same contract for the
// definition-only path, which shares the resolver.
func TestFindDefinition_TestsTrue_Resolves(t *testing.T) {
	dir := t.TempDir()
	writeTestsUniverseModule(t, dir)
	chdirInto(t, dir)

	h := queries.NewSurgeonQueriesHandler(nil)
	result, err := h.FindDefinition(context.Background(), domain.ReferencesQuery{
		Symbol: domain.SymbolRef{Name: "Helper"},
		Dir:    ".",
		Tests:  true,
	})
	require.NoError(t, err, "a uniquely-declared symbol must not be ambiguous with tests=true")
	assert.Equal(t, "lib.go", filepath.Base(result.Definition.File))
}
