package queries_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/queries"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeRefModule scaffolds a minimal two-file Go module so we can
// exercise go/packages-based resolution without reaching for the whole
// project tree.
func writeRefModule(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/refs\n\ngo 1.25\n"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "lib"), 0755))

	libSrc := `package lib

// Greeter has one method.
type Greeter struct {
	Name string
}

// Greet returns a greeting string using Name.
func (g *Greeter) Greet() string {
	return "hello, " + g.Name
}

// Helper is a free function that uses Greeter.
func Helper(g *Greeter) string {
	return g.Greet()
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib", "lib.go"), []byte(libSrc), 0644))

	mainSrc := `package main

import "example.com/refs/lib"

func main() {
	g := &lib.Greeter{Name: "world"}
	_ = g.Greet()
	_ = lib.Helper(g)
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainSrc), 0644))
}

func TestFindDefinition_Method(t *testing.T) {
	dir := t.TempDir()
	writeRefModule(t, dir)

	handler := queries.NewSurgeonQueriesHandler(nil)

	runInDir(t, dir, func() {
		res, err := handler.FindDefinition(context.Background(), domain.ReferencesQuery{
			Symbol: domain.SymbolRef{
				Name:     "Greet",
				Receiver: "Greeter",
			},
			Dir: ".",
		})
		require.NoError(t, err)
		assert.Equal(t, "method", res.Kind)
		assert.Equal(t, "Greet", res.Symbol.Name)
		assert.Equal(t, "Greeter", res.Symbol.Receiver)
		assert.Contains(t, res.Definition.File, filepath.Join("lib", "lib.go"))
		assert.Equal(t, 9, res.Definition.Line)
		assert.Greater(t, res.Definition.Column, 0)
	})
}

func TestFindDefinition_Type(t *testing.T) {
	dir := t.TempDir()
	writeRefModule(t, dir)

	handler := queries.NewSurgeonQueriesHandler(nil)

	runInDir(t, dir, func() {
		res, err := handler.FindDefinition(context.Background(), domain.ReferencesQuery{
			Symbol: domain.SymbolRef{Name: "Greeter"},
			Dir:    ".",
		})
		require.NoError(t, err)
		assert.Equal(t, "type", res.Kind)
		assert.Contains(t, res.Definition.File, filepath.Join("lib", "lib.go"))
		assert.Equal(t, 4, res.Definition.Line)
	})
}

func TestFindReferences_TypeAcrossPackages(t *testing.T) {
	dir := t.TempDir()
	writeRefModule(t, dir)

	handler := queries.NewSurgeonQueriesHandler(nil)

	runInDir(t, dir, func() {
		res, err := handler.FindReferences(context.Background(), domain.ReferencesQuery{
			Symbol: domain.SymbolRef{Name: "Greeter"},
			Dir:    ".",
		})
		require.NoError(t, err)
		// Expect at least: Helper's param, main's literal, method receiver types.
		require.NotEmpty(t, res.References)
		// Must find refs in both lib/lib.go and main.go.
		files := map[string]bool{}
		for _, loc := range res.References {
			files[filepath.Base(loc.File)] = true
		}
		assert.True(t, files["lib.go"], "expected a reference in lib.go")
		assert.True(t, files["main.go"], "expected a reference in main.go")
	})
}

func TestFindReferences_IncludeDefinition(t *testing.T) {
	dir := t.TempDir()
	writeRefModule(t, dir)

	handler := queries.NewSurgeonQueriesHandler(nil)

	runInDir(t, dir, func() {
		res, err := handler.FindReferences(context.Background(), domain.ReferencesQuery{
			Symbol:            domain.SymbolRef{Name: "Helper"},
			Dir:               ".",
			IncludeDefinition: true,
		})
		require.NoError(t, err)
		assert.Equal(t, "func", res.Kind)
		assert.NotEmpty(t, res.Definition.File)
		// Helper is called from main — expect at least one ref.
		require.NotEmpty(t, res.References)
	})
}

func TestFindDefinition_UnknownSymbol(t *testing.T) {
	dir := t.TempDir()
	writeRefModule(t, dir)

	handler := queries.NewSurgeonQueriesHandler(nil)

	runInDir(t, dir, func() {
		_, err := handler.FindDefinition(context.Background(), domain.ReferencesQuery{
			Symbol: domain.SymbolRef{Name: "DoesNotExist"},
			Dir:    ".",
		})
		require.Error(t, err)
	})
}

func TestFindDefinition_EmptyNameRejected(t *testing.T) {
	handler := queries.NewSurgeonQueriesHandler(nil)
	_, err := handler.FindDefinition(context.Background(), domain.ReferencesQuery{})
	require.Error(t, err)
}

// TestFindReferences_CachesPackagesLoad proves the shared loader cache
// is consulted by FindReferences: after one call primes the cache,
// a second identical call hits it instead of re-running packages.Load.
// This halves the wall-clock cost of the common agent workflow
// "find_references X then rename_symbol X Y".
func TestFindReferences_CachesPackagesLoad(t *testing.T) {
	dir := t.TempDir()
	writeRefModule(t, dir)

	handler := queries.NewSurgeonQueriesHandler(nil)

	runInDir(t, dir, func() {
		q := domain.ReferencesQuery{
			Symbol: domain.SymbolRef{Name: "Greet", Receiver: "Greeter"},
			Dir:    ".",
		}
		_, err := handler.FindReferences(context.Background(), q)
		require.NoError(t, err)
		assert.Equal(t, int64(0), handler.Loader().Hits())
		assert.Equal(t, int64(1), handler.Loader().Misses())

		_, err = handler.FindReferences(context.Background(), q)
		require.NoError(t, err)
		assert.Equal(t, int64(1), handler.Loader().Hits(), "second call should hit the cache")
		assert.Equal(t, int64(1), handler.Loader().Misses())
	})
}
