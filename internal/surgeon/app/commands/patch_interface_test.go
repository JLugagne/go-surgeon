package commands_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── add_method ────────────────────────────────────────────────────────────────

func TestPatchInterface_AddMethod(t *testing.T) {
	ctx := context.Background()

	t.Run("appends method", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type Reader interface {
	Read(p []byte) (int, error)
}
`)
		res, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "f.go",
			Identifier: "Reader",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpAddMethod, Signature: "Close() error"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, res.Applied)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "Close() error")
		readIdx := strings.Index(content, "Read(")
		closeIdx := strings.Index(content, "Close()")
		assert.True(t, readIdx < closeIdx, "Close should be appended after Read")
	})

	t.Run("inserts before anchor", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type Storage interface {
	Get(key string) ([]byte, error)
	Set(key string, v []byte) error
}
`)
		_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "f.go",
			Identifier: "Storage",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpAddMethod, Signature: "Delete(key string) error", Before: "Set"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		getIdx := strings.Index(content, "Get(")
		delIdx := strings.Index(content, "Delete(")
		setIdx := strings.Index(content, "Set(")
		assert.True(t, getIdx < delIdx && delIdx < setIdx)
	})

	t.Run("duplicate method errors without write", func(t *testing.T) {
		h, fs := newPatchHandler()
		src := `package p

type Reader interface {
	Read(p []byte) (int, error)
}
`
		setFile(fs, "f.go", src)
		_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "f.go",
			Identifier: "Reader",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpAddMethod, Signature: "Read(data []byte) error"},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
		assert.Equal(t, src, getFile(fs, "f.go"))
	})

	t.Run("invalid signature errors", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type Reader interface {
	Read() error
}
`)
		_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "f.go",
			Identifier: "Reader",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpAddMethod, Signature: "this is not valid Go"},
			},
		})
		require.Error(t, err)
	})
}

// ── remove_method ─────────────────────────────────────────────────────────────

func TestPatchInterface_RemoveMethod(t *testing.T) {
	ctx := context.Background()

	t.Run("removes named method", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type R interface {
	Read(p []byte) (int, error)
	Sync() error
	Close() error
}
`)
		_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "f.go",
			Identifier: "R",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpRemoveMethod, Name: "Sync"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.NotContains(t, content, "Sync()")
		assert.Contains(t, content, "Read(")
		assert.Contains(t, content, "Close()")
	})

	t.Run("remove_method on embed name errors", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

import "io"

type R interface {
	io.Closer
	Read(p []byte) (int, error)
}
`)
		_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "f.go",
			Identifier: "R",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpRemoveMethod, Name: "io.Closer"},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a method")
	})
}

// ── rename_method ─────────────────────────────────────────────────────────────

func TestPatchInterface_RenameMethod(t *testing.T) {
	ctx := context.Background()

	t.Run("renames method preserving signature and doc", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type R interface {
	// Read reads bytes.
	Read(p []byte) (int, error)
}
`)
		_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "f.go",
			Identifier: "R",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpRenameMethod, From: "Read", To: "ReadAt"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "ReadAt(p []byte) (int, error)")
		assert.NotContains(t, content, "Read(")
		assert.Contains(t, content, "// Read reads bytes.", "doc is preserved verbatim on rename")
	})
}

// ── retype_method ─────────────────────────────────────────────────────────────

func TestPatchInterface_RetypeMethod(t *testing.T) {
	ctx := context.Background()

	t.Run("retypes method signature", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type Storage interface {
	Get(key string) ([]byte, error)
}
`)
		_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "f.go",
			Identifier: "Storage",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpRetypeMethod, Name: "Get", Signature: "Get(ctx context.Context, key string) ([]byte, error)"},
			},
		})
		require.NoError(t, err)
		assert.Contains(t, getFile(fs, "f.go"), "Get(ctx context.Context, key string) ([]byte, error)")
	})

	t.Run("signature name mismatch errors", func(t *testing.T) {
		h, fs := newPatchHandler()
		src := `package p

type Storage interface {
	Get(key string) ([]byte, error)
}
`
		setFile(fs, "f.go", src)
		_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "f.go",
			Identifier: "Storage",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpRetypeMethod, Name: "Get", Signature: "Fetch(key string) ([]byte, error)"},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not match")
		assert.Equal(t, src, getFile(fs, "f.go"))
	})
}

// ── embed / remove_embed ─────────────────────────────────────────────────────

func TestPatchInterface_Embed(t *testing.T) {
	ctx := context.Background()

	t.Run("add embed", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type R interface {
	Read(p []byte) (int, error)
}
`)
		_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "f.go",
			Identifier: "R",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpEmbed, Type: "io.Closer", Position: "first"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "io.Closer")
		closerIdx := strings.Index(content, "io.Closer")
		readIdx := strings.Index(content, "Read(")
		assert.True(t, closerIdx < readIdx)
	})

	t.Run("remove embed by type literal", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

import "io"

type R interface {
	io.Closer
	Read(p []byte) (int, error)
}
`)
		_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "f.go",
			Identifier: "R",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpRemoveEmbed, Type: "io.Closer"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.NotContains(t, content, "io.Closer")
		assert.Contains(t, content, "Read(")
	})

	t.Run("remove_embed on method name errors", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type R interface {
	Close() error
}
`)
		_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "f.go",
			Identifier: "R",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpRemoveEmbed, Type: "Close"},
			},
		})
		require.Error(t, err)
	})

	t.Run("duplicate embed errors", func(t *testing.T) {
		h, fs := newPatchHandler()
		src := `package p

import "io"

type R interface {
	io.Closer
}
`
		setFile(fs, "f.go", src)
		_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "f.go",
			Identifier: "R",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpEmbed, Type: "io.Closer"},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already present")
	})
}

// ── set_doc ───────────────────────────────────────────────────────────────────

func TestPatchInterface_SetDoc(t *testing.T) {
	ctx := context.Background()

	h, fs := newPatchHandler()
	setFile(fs, "f.go", `package p

type R interface {
	Close() error
}
`)
	_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
		FilePath:   "f.go",
		Identifier: "R",
		Patches: []domain.InterfacePatch{
			{Op: domain.InterfacePatchOpSetDoc, Name: "Close", Doc: "Close releases resources."},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, getFile(fs, "f.go"), "// Close releases resources.")
}

// ── atomicity ─────────────────────────────────────────────────────────────────

func TestPatchInterface_Atomicity(t *testing.T) {
	ctx := context.Background()

	src := `package p

type R interface {
	A() error
	B() error
}
`

	t.Run("multi-patch all succeed", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", src)
		_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "f.go",
			Identifier: "R",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpAddMethod, Signature: "C() error"},
				{Op: domain.InterfacePatchOpRemoveMethod, Name: "B"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "A()")
		assert.Contains(t, content, "C()")
		assert.NotContains(t, content, "B()")
	})

	t.Run("one bad patch writes nothing", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", src)
		_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "f.go",
			Identifier: "R",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpAddMethod, Signature: "C() error"},
				{Op: domain.InterfacePatchOpRemoveMethod, Name: "DoesNotExist"},
			},
		})
		require.Error(t, err)
		assert.Equal(t, src, getFile(fs, "f.go"))
	})
}

// ── preview ───────────────────────────────────────────────────────────────────

func TestPatchInterface_Preview(t *testing.T) {
	ctx := context.Background()

	h, fs := newPatchHandler()
	src := `package p

type R interface {
	Close() error
}
`
	setFile(fs, "f.go", src)
	res, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
		FilePath:   "f.go",
		Identifier: "R",
		Patches: []domain.InterfacePatch{
			{Op: domain.InterfacePatchOpAddMethod, Signature: "Sync() error"},
		},
		Preview: true,
	})
	require.NoError(t, err)
	assert.True(t, res.Preview)
	assert.NotEmpty(t, res.Diff)
	assert.Equal(t, src, getFile(fs, "f.go"), "preview must not write")
}

// ── mock regeneration ────────────────────────────────────────────────────────

func TestPatchInterface_MockRegeneration(t *testing.T) {
	ctx := context.Background()

	t.Run("regenerates mock when method set changes and mock_file+mock_name are set", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "reader.go", `package p

type Reader interface {
	Read(p []byte) (int, error)
}
`)
		// Seed an existing mock file so the regeneration has a target.
		setFile(fs, "mock.go", `package p

type MockReader struct {
	ReadFunc func(p []byte) (int, error)
}
`)
		res, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "reader.go",
			Identifier: "Reader",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpAddMethod, Signature: "Close() error"},
			},
			MockFile: "mock.go",
			MockName: "MockReader",
		})
		require.NoError(t, err)
		assert.True(t, res.MockUpdated, "mock should be regenerated when the method set changes")
		mockContent := getFile(fs, "mock.go")
		assert.Contains(t, mockContent, "CloseFunc", "mock should include a CloseFunc field")
		assert.Contains(t, mockContent, "ReadFunc")
	})

	t.Run("skips mock regeneration when only doc changes", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "reader.go", `package p

type Reader interface {
	Read(p []byte) (int, error)
}
`)
		setFile(fs, "mock.go", `package p

type MockReader struct {
	ReadFunc func(p []byte) (int, error)
}
`)
		origMock := getFile(fs, "mock.go")
		res, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "reader.go",
			Identifier: "Reader",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpSetDoc, Name: "Read", Doc: "Read reads bytes."},
			},
			MockFile: "mock.go",
			MockName: "MockReader",
		})
		require.NoError(t, err)
		assert.False(t, res.MockUpdated, "set_doc must not trigger mock regeneration")
		assert.Equal(t, origMock, getFile(fs, "mock.go"))
	})

	t.Run("skips mock regeneration when mock_file/mock_name not provided", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "reader.go", `package p

type Reader interface {
	Read(p []byte) (int, error)
}
`)
		res, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "reader.go",
			Identifier: "Reader",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpAddMethod, Signature: "Close() error"},
			},
		})
		require.NoError(t, err)
		assert.False(t, res.MockUpdated)
	})
}

// ── not found ─────────────────────────────────────────────────────────────────

func TestPatchInterface_NotFound(t *testing.T) {
	ctx := context.Background()

	h, fs := newPatchHandler()
	setFile(fs, "f.go", "package p\n")
	_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
		FilePath:   "f.go",
		Identifier: "Missing",
		Patches: []domain.InterfacePatch{
			{Op: domain.InterfacePatchOpAddMethod, Signature: "X() error"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestPatchInterface_PreservesInlineComments(t *testing.T) {
	ctx := context.Background()

	src := `package p

type Reader interface {
	Read(p []byte) (int, error) // core read method
	Close() error               // release resources
}
`

	t.Run("add_method preserves sibling inline comments", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", src)
		_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "f.go",
			Identifier: "Reader",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpAddMethod, Signature: "Seek(offset int64) (int64, error)"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "// core read method")
		assert.Contains(t, content, "// release resources")
	})

	t.Run("rename_method preserves inline comment", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", src)
		_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "f.go",
			Identifier: "Reader",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpRenameMethod, From: "Read", To: "ReadBytes"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "// core read method", "inline comment should move with renamed method")
		assert.Contains(t, content, "// release resources")
	})

	t.Run("retype_method preserves inline comment", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", src)
		_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "f.go",
			Identifier: "Reader",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpRetypeMethod, Name: "Read", Signature: "Read(buf []byte) (int, error)"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "// core read method")
		assert.Contains(t, content, "// release resources")
	})

	t.Run("remove_method preserves other inline comments", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", src)
		_, err := h.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   "f.go",
			Identifier: "Reader",
			Patches: []domain.InterfacePatch{
				{Op: domain.InterfacePatchOpRemoveMethod, Name: "Close"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "// core read method")
	})
}
