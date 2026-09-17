package loader_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/loader"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// TestLoader_ReleasesDependencySyntaxAndTypeInfo pins the heap contract:
// after Load, dependency packages must no longer retain their parsed
// syntax or per-file type info. With NeedDeps go/packages parses and
// type-checks the whole dependency graph from source, and on a real
// module those payloads are ~80% of the cached graph even though every
// loader consumer only reads the root packages.
func TestLoader_ReleasesDependencySyntaxAndTypeInfo(t *testing.T) {
	depDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(depDir, "go.mod"), []byte("module example.com/dep\n\ngo 1.25\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(depDir, "dep.go"), []byte("package dep\n\nfunc Value() int { return 1 }\n"), 0644))

	mainDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(mainDir, "go.mod"), []byte(
		"module example.com/main\n\ngo 1.25\n\nrequire example.com/dep v0.0.0\n\nreplace example.com/dep => "+depDir+"\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(mainDir, "main.go"), []byte(
		"package main\n\nimport \"example.com/dep\"\n\nfunc main() { _ = dep.Value() }\n"), 0644))

	l := loader.New()
	loaded, err := l.Load(context.Background(), mainDir, false)
	require.NoError(t, err)

	var root *packages.Package
	for _, p := range loaded.Pkgs {
		if p.PkgPath == "example.com/main" {
			root = p
		}
	}
	require.NotNil(t, root, "root package must be returned by Load")
	require.NotEmpty(t, root.Syntax, "root syntax must be retained")
	require.NotNil(t, root.TypesInfo, "root type info must be retained")

	dep := root.Imports["example.com/dep"]
	require.NotNil(t, dep, "dependency must be reachable through Imports")
	assert.Nil(t, dep.Syntax, "dependency syntax must be released before caching")
	assert.Nil(t, dep.TypesInfo, "dependency type info must be released before caching")
	assert.NotNil(t, dep.Types, "dependency types must stay reachable through the roots")
}
