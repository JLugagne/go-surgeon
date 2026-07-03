package commands_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/outbound/filesystem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeSingleFileModule(t *testing.T, dir, module, src string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module "+module+"\n\ngo 1.25\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0644))
}

// TestRename_NestedScopeCapture_InboundRejected covers the direction
// where a reference to the target lives inside a nested scope that
// already declares the new name. Renaming count -> total silently
// rebinds `count` (inside the block) to the block-local total; the code
// still compiles, so build_check never catches it. checkNoCollision only
// inspected the target's parent scope (which has no `total`), so the
// rename was allowed. The fix walks the scope of every reference site and
// rejects with CONFLICT.
func TestRename_NestedScopeCapture_InboundRejected(t *testing.T) {
	dir := t.TempDir()
	src := `package main

func f() int {
	count := 1
	{
		total := 10
		count = count + total
		_ = total
	}
	return count
}

func main() {
	_ = f()
}
`
	writeSingleFileModule(t, dir, "example.com/capture", src)

	handler := commands.NewExecutePlanHandler(filesystem.NewFileSystem())

	runInDir(t, dir, func() {
		_, err := handler.Rename(context.Background(), domain.RenameRequest{
			Symbol:  domain.SymbolRef{Name: "count"},
			NewName: "total",
			Dir:     ".",
		})
		require.Error(t, err, "renaming count->total captures the nested reference; must be rejected")
		var derr *domain.Error
		require.ErrorAs(t, err, &derr)
		assert.Equal(t, "CONFLICT", derr.Code)
	})
}

// TestRename_NestedScopeCapture_OutboundRejected covers the other
// direction: renaming a nested-scope variable to a name declared in an
// enclosing scope that is referenced from within the nested scope. Here
// renaming inner `tmp` -> outer `total` makes the renamed variable shadow
// the outer total for the `_ = total` reference inside the block, silently
// rebinding it. Must be rejected with CONFLICT.
func TestRename_NestedScopeCapture_OutboundRejected(t *testing.T) {
	dir := t.TempDir()
	src := `package main

func f() int {
	total := 1
	{
		tmp := 10
		_ = total
		total = tmp
	}
	return total
}

func main() {
	_ = f()
}
`
	writeSingleFileModule(t, dir, "example.com/captureout", src)

	handler := commands.NewExecutePlanHandler(filesystem.NewFileSystem())

	runInDir(t, dir, func() {
		_, err := handler.Rename(context.Background(), domain.RenameRequest{
			Symbol:  domain.SymbolRef{Name: "tmp"},
			NewName: "total",
			Dir:     ".",
		})
		require.Error(t, err, "renaming tmp->total captures the outer total reference; must be rejected")
		var derr *domain.Error
		require.ErrorAs(t, err, &derr)
		assert.Equal(t, "CONFLICT", derr.Code)
	})
}

// TestRename_HarmlessShadow_NotRejected guards against over-rejection:
// a nested scope may declare the new name as long as no reference changes
// binding. Here the outer `count` is never referenced inside the block, so
// renaming it to `total` is safe and must be allowed.
func TestRename_HarmlessShadow_NotRejected(t *testing.T) {
	dir := t.TempDir()
	src := `package main

func f() int {
	count := 1
	{
		total := 10
		_ = total
	}
	return count
}

func main() {
	_ = f()
}
`
	writeSingleFileModule(t, dir, "example.com/harmless", src)

	handler := commands.NewExecutePlanHandler(filesystem.NewFileSystem())

	runInDir(t, dir, func() {
		result, err := handler.Rename(context.Background(), domain.RenameRequest{
			Symbol:  domain.SymbolRef{Name: "count"},
			NewName: "total",
			Dir:     ".",
		})
		require.NoError(t, err, "harmless shadow must not be rejected")
		assert.Equal(t, "var", result.Kind)
		mainBytes, err := os.ReadFile(filepath.Join(dir, "main.go"))
		require.NoError(t, err)
		assert.True(t, strings.Contains(string(mainBytes), "return total"))
	})
}
