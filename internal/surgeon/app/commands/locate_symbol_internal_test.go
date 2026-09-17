package commands

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// TestLocateSymbol_CollapsesSameLineMethodDuplicates reproduces issue #36:
// the resolver can see the same method declaration twice with positions
// that differ only by column on one line (e.g. the plain and the
// test-augmented package universes). It must not report that as
// ambiguous, and receiver+name must resolve it.
func TestLocateSymbol_CollapsesSameLineMethodDuplicates(t *testing.T) {
	fset := token.NewFileSet()
	src := "package p\n\ntype ApplyReport struct{}\n\nfunc (r *ApplyReport) WriteTo() {}\n"
	f, err := parser.ParseFile(fset, "/virtual/apply.go", src, 0)
	require.NoError(t, err)

	info := &types.Info{
		Defs: map[*ast.Ident]types.Object{},
		Uses: map[*ast.Ident]types.Object{},
	}
	tpkg, err := (&types.Config{}).Check("example.com/p", fset, []*ast.File{f}, info)
	require.NoError(t, err)

	// Locate the real method object and the receiver-type identifier, which
	// sits on the same source line as the method name.
	var methodObj types.Object
	var methodIdent *ast.Ident
	var recvPos token.Pos
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "WriteTo" || fd.Recv == nil || len(fd.Recv.List) == 0 {
			continue
		}
		methodIdent = fd.Name
		methodObj = info.Defs[fd.Name]
		rt := fd.Recv.List[0].Type
		if star, ok := rt.(*ast.StarExpr); ok {
			rt = star.X
		}
		if id, ok := rt.(*ast.Ident); ok {
			recvPos = id.Pos()
		}
	}
	require.NotNil(t, methodObj)
	require.NotNil(t, methodIdent)
	require.True(t, recvPos.IsValid())
	require.Equal(t, fset.Position(methodIdent.Pos()).Line, fset.Position(recvPos).Line,
		"test setup expects the receiver and method name on one line")

	fn, ok := methodObj.(*types.Func)
	require.True(t, ok)
	sig, ok := fn.Type().(*types.Signature)
	require.True(t, ok)

	// Simulate the duplicate universe: a second ident on the same line but
	// a different column, bound to a distinct object of the same method.
	clone := types.NewFunc(recvPos, tpkg, "WriteTo", sig)
	cloneIdent := &ast.Ident{Name: "WriteTo", NamePos: recvPos}
	info.Defs[cloneIdent] = clone

	pkg := &packages.Package{
		PkgPath:   "example.com/p",
		Name:      "p",
		Types:     tpkg,
		TypesInfo: info,
	}

	obj, kind, err := locateSymbol(fset, []*packages.Package{pkg}, domain.SymbolRef{
		Name:     "WriteTo",
		Receiver: "ApplyReport",
	})
	require.NoError(t, err, "same-line duplicates must not be reported as ambiguous")
	assert.Equal(t, "method", kind)
	assert.NotNil(t, obj)
}
