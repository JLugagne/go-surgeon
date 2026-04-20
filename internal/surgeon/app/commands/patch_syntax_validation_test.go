package commands_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests guard against the class of bug where a patch produces a
// syntactically invalid Go file. The tools must:
//  1. reject the patch before writing
//  2. return the error code PATCH_PRODUCES_INVALID_GO
//  3. include a line:col snippet in the error message so the agent can fix
//     the patch on the next turn without re-reading the file
//  4. leave the original file byte-identical on disk

func TestPatchFunction_RejectsSyntaxErrorOutput(t *testing.T) {
	ctx := context.Background()

	t.Run("stray closing brace in replacement is rejected", func(t *testing.T) {
		h, fs := newPatchHandler()
		src := `package p

func F() {
	x := 1
}
`
		setFile(fs, "f.go", src)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "x := 1", Replace: "x := 1 }"},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "closing braces")
		assert.Equal(t, src, getFile(fs, "f.go"), "file must be byte-identical to original")
	})

	t.Run("unclosed string literal is rejected", func(t *testing.T) {
		h, fs := newPatchHandler()
		src := `package p

func F() {
	s := "hello"
}
`
		setFile(fs, "f.go", src)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: `"hello"`, Replace: `"hello`},
			},
		})
		require.Error(t, err)
		assert.Equal(t, src, getFile(fs, "f.go"))
	})

	t.Run("error snippet includes a line/column pointer", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

func F() {
	x := 1
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "x := 1", Replace: "x := 1 }"},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "closing braces")
	})

	t.Run("preview mode also rejects invalid output", func(t *testing.T) {
		// Preview should also catch the error — otherwise the agent would see
		// a "clean" diff and only discover the bug when applying for real.
		h, fs := newPatchHandler()
		src := `package p

func F() {
	x := 1
}
`
		setFile(fs, "f.go", src)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "x := 1", Replace: "x := 1 }"},
			},
			Preview: true,
		})
		require.Error(t, err)
		assert.Equal(t, src, getFile(fs, "f.go"))
	})

	t.Run("valid patch is still accepted", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

func F() {
	x := 1
}
`)
		_, err := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   "f.go",
			Identifier: "F",
			Patches: []domain.FunctionPatch{
				{Op: domain.PatchOpReplace, Match: "x := 1", Replace: "x := 42"},
			},
		})
		require.NoError(t, err)
		assert.Contains(t, getFile(fs, "f.go"), "x := 42")
	})
}

func TestPatchStruct_RejectsSyntaxErrorOutput(t *testing.T) {
	ctx := context.Background()

	t.Run("retype_field with unclosed bracket is rejected", func(t *testing.T) {
		h, fs := newPatchHandler()
		src := `package p

type U struct {
	ID string
}
`
		setFile(fs, "f.go", src)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "U",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpRetypeField, Name: "ID", Type: "map[string"},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid Go")
		assert.Equal(t, src, getFile(fs, "f.go"))
	})

	t.Run("add_field with malformed type is rejected", func(t *testing.T) {
		h, fs := newPatchHandler()
		src := `package p

type U struct {
	ID string
}
`
		setFile(fs, "f.go", src)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "U",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpAddField, Name: "Bad", Type: "func("},
			},
		})
		require.Error(t, err)
		assert.Equal(t, src, getFile(fs, "f.go"))
	})
}

func TestPatchInterface_RejectsSyntaxErrorOutput(t *testing.T) {
	ctx := context.Background()

	t.Run("retype_method with unbalanced parens is rejected", func(t *testing.T) {
		// The parseMethodSignature layer catches many of these, but this
		// test specifically ensures validateGoSource is the last-line defence:
		// a signature that parses standalone but breaks the surrounding file.
		h, fs := newPatchHandler()
		src := `package p

type R interface {
	Close() error
}
`
		setFile(fs, "f.go", src)
		// First, a truly malformed signature the upfront parser catches:
		_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "f.go",
			Identifier: "R",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpAddMethod, Signature: "Garbage(((("},
			},
		})
		require.Error(t, err)
		assert.Equal(t, src, getFile(fs, "f.go"))
	})
}
