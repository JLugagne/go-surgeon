package commands_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// structFieldSnapshot is one field as seen after re-parsing the patched file.
type structFieldSnapshot struct {
	typeExpr string
	tag      string
}

// snapshotStructFields re-parses src and returns name -> {type, tag} for the
// named struct, so tests can assert semantics independent of line grouping.
func snapshotStructFields(t *testing.T, src, structName string) map[string]structFieldSnapshot {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "snap.go", src, parser.ParseComments)
	require.NoError(t, err, "patched output must be valid Go:\n%s", src)

	out := map[string]structFieldSnapshot{}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != structName {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range st.Fields.List {
				var tag string
				if field.Tag != nil {
					tag = strings.Trim(field.Tag.Value, "`")
				}
				for _, n := range field.Names {
					out[n.Name] = structFieldSnapshot{typeExpr: types.ExprString(field.Type), tag: tag}
				}
			}
		}
	}
	return out
}

// Backlog item 6: renderElements rebuilds the struct body from the element
// list only, so free-standing comments (not attached to any field) and the
// blank lines that group fields vanish on ANY patch.
func TestPatchStruct_PreservesFreeStandingCommentsAndBlankLines(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

type S struct {
	A int

	// --- group two ---

	B string
}
`)

	_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
		FilePath:   "f.go",
		Identifier: "S",
		Patches: []domain.StructPatch{
			{Op: domain.StructPatchOpSetTag, Name: "A", Tag: `json:"a"`},
		},
	})
	require.NoError(t, err)

	content := getFile(fs, "f.go")
	assert.Contains(t, content, "// --- group two ---",
		"free-standing comment inside the struct must survive an unrelated patch")
	assert.Contains(t, content, "\n\n\t// --- group two ---\n\n\tB string",
		"blank-line grouping around the free-standing comment must be preserved")
}

// Backlog item 14: in a single-line struct the field's raw line captures the
// braces (`type W struct{ inner http.Handler }`), so any patch re-renders
// the whole declaration into garbage or is rejected.
func TestPatchStruct_AddFieldToSingleLineStruct(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

import "net/http"

type W struct{ inner http.Handler }
`)

	_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
		FilePath:   "f.go",
		Identifier: "W",
		Patches: []domain.StructPatch{
			{Op: domain.StructPatchOpAddField, Name: "New", Type: "string"},
		},
	})
	require.NoError(t, err, "add_field on a single-line struct must succeed")

	content := getFile(fs, "f.go")
	fields := snapshotStructFields(t, content, "W")
	assert.Equal(t, "http.Handler", fields["inner"].typeExpr)
	assert.Equal(t, "string", fields["New"].typeExpr)
	assert.Equal(t, 1, strings.Count(content, "struct"),
		"the struct declaration must not be duplicated:\n%s", content)
}

// Backlog item 29: a grouped field declaration (X, Y int) untouched by the
// patch must stay grouped and keep its doc comment exactly once.
func TestPatchStruct_UntouchedGroupedFieldStaysGrouped(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

type P struct {
	// coordinates
	X, Y int
	Name string
}
`)

	_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
		FilePath:   "f.go",
		Identifier: "P",
		Patches: []domain.StructPatch{
			{Op: domain.StructPatchOpSetTag, Name: "Name", Tag: `json:"name"`},
		},
	})
	require.NoError(t, err)

	content := getFile(fs, "f.go")
	assert.Contains(t, content, "X, Y int",
		"a grouped field not targeted by the patch must stay grouped")
	assert.Equal(t, 1, strings.Count(content, "// coordinates"),
		"the group's doc comment must not be duplicated:\n%s", content)
}

// Backlog item 29: renaming one member of a grouped field must not duplicate
// the group's doc comment nor corrupt the remaining members.
func TestPatchStruct_RenameGroupedMemberNoDocDuplication(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

type P struct {
	// coordinates
	X, Y int
	Name string
}
`)

	_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
		FilePath:   "f.go",
		Identifier: "P",
		Patches: []domain.StructPatch{
			{Op: domain.StructPatchOpRenameField, From: "Y", To: "Z"},
		},
	})
	require.NoError(t, err)

	content := getFile(fs, "f.go")
	fields := snapshotStructFields(t, content, "P")
	assert.NotContains(t, fields, "Y", "renamed member must be gone")
	assert.Equal(t, "int", fields["X"].typeExpr)
	assert.Equal(t, "int", fields["Z"].typeExpr)
	assert.Equal(t, "string", fields["Name"].typeExpr)
	assert.Equal(t, 1, strings.Count(content, "// coordinates"),
		"the group's doc comment must appear exactly once:\n%s", content)
}

// Backlog item 29: tagging one member of a grouped field must split the tag
// onto that member only, without duplicating the shared doc comment.
func TestPatchStruct_SetTagOnGroupedMemberSplitsCleanly(t *testing.T) {
	ctx := context.Background()
	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

type P struct {
	// coordinates
	X, Y int
}
`)

	_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
		FilePath:   "f.go",
		Identifier: "P",
		Patches: []domain.StructPatch{
			{Op: domain.StructPatchOpSetTag, Name: "Y", Tag: `json:"y"`},
		},
	})
	require.NoError(t, err)

	content := getFile(fs, "f.go")
	fields := snapshotStructFields(t, content, "P")
	assert.Equal(t, `json:"y"`, fields["Y"].tag, "targeted member must carry the tag")
	assert.Empty(t, fields["X"].tag, "untargeted member must not inherit the tag")
	assert.Equal(t, 1, strings.Count(content, "// coordinates"),
		"the group's doc comment must appear exactly once:\n%s", content)
}
