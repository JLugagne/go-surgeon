package commands_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/app/loader"
	"github.com/JLugagne/go-surgeon/internal/surgeon/app/queries"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/outbound/filesystem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeRenameModule scaffolds a two-file Go module exercising
// cross-package renames: Greeter and its Greet method are used from
// main.go, so a rename of either must rewrite both files.
func writeRenameModule(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/rename\n\ngo 1.25\n"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "lib"), 0755))

	libSrc := `package lib

type Greeter struct {
	Name string
}

func (g *Greeter) Greet() string {
	return "hello, " + g.Name
}

func Helper(g *Greeter) string {
	return g.Greet()
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib", "lib.go"), []byte(libSrc), 0644))

	mainSrc := `package main

import "example.com/rename/lib"

func main() {
	g := &lib.Greeter{Name: "world"}
	_ = g.Greet()
	_ = lib.Helper(g)
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainSrc), 0644))
}

// runInDir chdirs into dir for the duration of fn so go/packages resolves
// relative to the temp module.
func runInDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	prev, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(prev) })
	fn()
}

func TestRename_Type_RewritesAllReferences(t *testing.T) {
	dir := t.TempDir()
	writeRenameModule(t, dir)

	handler := commands.NewExecutePlanHandler(filesystem.NewFileSystem())

	runInDir(t, dir, func() {
		result, err := handler.Rename(context.Background(), domain.RenameRequest{
			Symbol:  domain.SymbolRef{Name: "Greeter"},
			NewName: "Welcomer",
			Dir:     ".",
		})
		require.NoError(t, err)
		assert.Equal(t, "type", result.Kind)
		assert.GreaterOrEqual(t, len(result.FilesModified), 2, "expected rewrites in both lib.go and main.go")

		libBytes, err := os.ReadFile(filepath.Join(dir, "lib", "lib.go"))
		require.NoError(t, err)
		assert.Contains(t, string(libBytes), "type Welcomer struct")
		assert.NotContains(t, string(libBytes), "type Greeter struct")
		assert.Contains(t, string(libBytes), "*Welcomer")

		mainBytes, err := os.ReadFile(filepath.Join(dir, "main.go"))
		require.NoError(t, err)
		assert.Contains(t, string(mainBytes), "lib.Welcomer")
		assert.NotContains(t, string(mainBytes), "lib.Greeter")
	})
}

func TestRename_Method_UpdatesCallSites(t *testing.T) {
	dir := t.TempDir()
	writeRenameModule(t, dir)

	handler := commands.NewExecutePlanHandler(filesystem.NewFileSystem())

	runInDir(t, dir, func() {
		result, err := handler.Rename(context.Background(), domain.RenameRequest{
			Symbol:  domain.SymbolRef{Name: "Greet", Receiver: "Greeter"},
			NewName: "Salute",
			Dir:     ".",
		})
		require.NoError(t, err)
		assert.Equal(t, "method", result.Kind)

		libBytes, err := os.ReadFile(filepath.Join(dir, "lib", "lib.go"))
		require.NoError(t, err)
		assert.Contains(t, string(libBytes), "func (g *Greeter) Salute()")
		assert.NotContains(t, string(libBytes), "Greet(")
		assert.Contains(t, string(libBytes), "g.Salute()")

		mainBytes, err := os.ReadFile(filepath.Join(dir, "main.go"))
		require.NoError(t, err)
		assert.Contains(t, string(mainBytes), "g.Salute()")
	})
}

func TestRename_Preview_DoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	writeRenameModule(t, dir)

	originalLib, err := os.ReadFile(filepath.Join(dir, "lib", "lib.go"))
	require.NoError(t, err)
	originalMain, err := os.ReadFile(filepath.Join(dir, "main.go"))
	require.NoError(t, err)

	handler := commands.NewExecutePlanHandler(filesystem.NewFileSystem())

	runInDir(t, dir, func() {
		result, err := handler.Rename(context.Background(), domain.RenameRequest{
			Symbol:  domain.SymbolRef{Name: "Greeter"},
			NewName: "Welcomer",
			Dir:     ".",
			DryRun:  true,
		})
		require.NoError(t, err)
		assert.True(t, result.DryRun)
		assert.NotEmpty(t, result.Locations)

		// Files on disk must be untouched.
		libAfter, err := os.ReadFile(filepath.Join(dir, "lib", "lib.go"))
		require.NoError(t, err)
		mainAfter, err := os.ReadFile(filepath.Join(dir, "main.go"))
		require.NoError(t, err)
		assert.Equal(t, string(originalLib), string(libAfter))
		assert.Equal(t, string(originalMain), string(mainAfter))
	})
}

func TestRename_ExportStatusChangeRejected(t *testing.T) {
	dir := t.TempDir()
	writeRenameModule(t, dir)

	handler := commands.NewExecutePlanHandler(filesystem.NewFileSystem())

	runInDir(t, dir, func() {
		_, err := handler.Rename(context.Background(), domain.RenameRequest{
			Symbol:  domain.SymbolRef{Name: "Greeter"},
			NewName: "greeter",
			Dir:     ".",
		})
		require.Error(t, err)
		assert.True(t, strings.Contains(err.Error(), "export status"))
		// The hint should point users at the escape hatch.
		assert.Contains(t, err.Error(), "allow_export_change")
	})
}

// TestRename_ExportStatusChange_AllowedWithFlag proves the escape hatch:
// when AllowExportChange=true, a case-flip rename (Exported -> unexported)
// proceeds and the result carries a warning naming both the old and new
// export status so agents notice what they just did.
func TestRename_ExportStatusChange_AllowedWithFlag(t *testing.T) {
	dir := t.TempDir()
	writeRenameModule(t, dir)

	handler := commands.NewExecutePlanHandler(filesystem.NewFileSystem())

	runInDir(t, dir, func() {
		result, err := handler.Rename(context.Background(), domain.RenameRequest{
			Symbol:            domain.SymbolRef{Name: "Greeter"},
			NewName:           "greeter",
			Dir:               ".",
			AllowExportChange: true,
			DryRun:            true,
		})
		require.NoError(t, err)
		assert.Equal(t, "type", result.Kind)
		require.NotEmpty(t, result.Warnings, "expected a warning about the export flip")
		assert.Contains(t, result.Warnings[0], "export status changed")
		assert.Contains(t, result.Warnings[0], "\"Greeter\"")
		assert.Contains(t, result.Warnings[0], "exported")
		assert.Contains(t, result.Warnings[0], "\"greeter\"")
		assert.Contains(t, result.Warnings[0], "unexported")
	})
}

// TestRename_ExportStatusChange_UnexportedToExported_Allowed covers the
// other direction (foo -> Foo) under the same flag, confirming the
// warning describes the flip the other way around.
func TestRename_ExportStatusChange_UnexportedToExported_Allowed(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/lowercase\n\ngo 1.25\n"), 0644))
	src := `package main

func greet() string {
	return "hi"
}

func main() {
	_ = greet()
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0644))

	handler := commands.NewExecutePlanHandler(filesystem.NewFileSystem())

	runInDir(t, dir, func() {
		result, err := handler.Rename(context.Background(), domain.RenameRequest{
			Symbol:            domain.SymbolRef{Name: "greet"},
			NewName:           "Greet",
			Dir:               ".",
			AllowExportChange: true,
			DryRun:            true,
		})
		require.NoError(t, err)
		require.NotEmpty(t, result.Warnings)
		assert.Contains(t, result.Warnings[0], "\"greet\"")
		assert.Contains(t, result.Warnings[0], "unexported")
		assert.Contains(t, result.Warnings[0], "\"Greet\"")
		assert.Contains(t, result.Warnings[0], "exported")
	})
}

func TestRename_InvalidIdentifierRejected(t *testing.T) {
	dir := t.TempDir()
	writeRenameModule(t, dir)

	handler := commands.NewExecutePlanHandler(filesystem.NewFileSystem())

	runInDir(t, dir, func() {
		_, err := handler.Rename(context.Background(), domain.RenameRequest{
			Symbol:  domain.SymbolRef{Name: "Greeter"},
			NewName: "1Bad",
			Dir:     ".",
		})
		require.Error(t, err)
	})
}

func TestRename_SameNameRejected(t *testing.T) {
	handler := commands.NewExecutePlanHandler(filesystem.NewFileSystem())
	_, err := handler.Rename(context.Background(), domain.RenameRequest{
		Symbol:  domain.SymbolRef{Name: "X"},
		NewName: "X",
	})
	require.Error(t, err)
}

func TestRename_EmptyNewNameRejected(t *testing.T) {
	handler := commands.NewExecutePlanHandler(filesystem.NewFileSystem())
	_, err := handler.Rename(context.Background(), domain.RenameRequest{
		Symbol:  domain.SymbolRef{Name: "X"},
		NewName: "",
	})
	require.Error(t, err)
}

// TestRename_SharesLoaderCacheWithQueries proves the cache is shared
// across handlers when the same *loader.Loader is wired into both.
// This is the hot-path payoff: a typical workflow primes the cache in
// FindReferences (via the queries handler) and then Rename (via the
// commands handler) reuses it — saving a full packages.Load.
func TestRename_SharesLoaderCacheWithQueries(t *testing.T) {
	dir := t.TempDir()
	writeRenameModule(t, dir)

	shared := loader.New()
	renamer := commands.NewExecutePlanHandler(filesystem.NewFileSystem()).WithLoader(shared)
	querier := queries.NewSurgeonQueriesHandler(nil).WithLoader(shared)

	runInDir(t, dir, func() {
		// Prime via the queries handler.
		_, err := querier.FindReferences(context.Background(), domain.ReferencesQuery{
			Symbol: domain.SymbolRef{Name: "Greeter"},
			Dir:    ".",
		})
		require.NoError(t, err)
		assert.Equal(t, int64(1), shared.Misses())
		assert.Equal(t, int64(0), shared.Hits())

		// The commands handler should now hit the shared cache.
		_, err = renamer.Rename(context.Background(), domain.RenameRequest{
			Symbol:  domain.SymbolRef{Name: "Greeter"},
			NewName: "Welcomer",
			Dir:     ".",
			DryRun:  true,
		})
		require.NoError(t, err)
		assert.Equal(t, int64(1), shared.Hits(), "Rename should reuse the cache primed by FindReferences")
		assert.Equal(t, int64(1), shared.Misses())
	})
}
