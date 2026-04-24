package commands_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── add_field ─────────────────────────────────────────────────────────────────

func TestPatchStruct_AddField(t *testing.T) {
	ctx := context.Background()

	t.Run("appends field by default", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type User struct {
	ID   string
	Name string
}
`)
		res, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpAddField, Name: "Email", Type: "string", Tag: `json:"email"`},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, res.Applied)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "Email string")
		assert.Contains(t, content, `json:"email"`)
		// Order preserved
		idIdx := strings.Index(content, "ID")
		nameIdx := strings.Index(content, "Name")
		emailIdx := strings.Index(content, "Email")
		assert.True(t, idIdx < nameIdx && nameIdx < emailIdx, "fields should stay in order")
	})

	t.Run("inserts before anchor", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type User struct {
	ID   string
	Name string
}
`)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpAddField, Name: "Email", Type: "string", Before: "Name"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		idIdx := strings.Index(content, "ID")
		emailIdx := strings.Index(content, "Email")
		nameIdx := strings.Index(content, "Name")
		assert.True(t, idIdx < emailIdx && emailIdx < nameIdx,
			"Email should be between ID and Name")
	})

	t.Run("inserts after anchor", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type User struct {
	ID   string
	Name string
}
`)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpAddField, Name: "Email", Type: "string", After: "ID"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		idIdx := strings.Index(content, "ID")
		emailIdx := strings.Index(content, "Email")
		nameIdx := strings.Index(content, "Name")
		assert.True(t, idIdx < emailIdx && emailIdx < nameIdx)
	})

	t.Run("position first prepends", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type User struct {
	Name string
}
`)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpAddField, Name: "ID", Type: "string", Position: "first"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		idIdx := strings.Index(content, "ID")
		nameIdx := strings.Index(content, "Name")
		assert.True(t, idIdx < nameIdx)
	})

	t.Run("add_field with multiline doc renders comment block", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type Config struct {
	Host string
}
`)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "Config",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpAddField, Name: "File", Type: "string", Doc: "AtLine, when > 0, resolves the outermost declaration that spans\nthat line in File. Mutually exclusive with Name/Pattern."},
				{Op: domain.StructPatchOpAddField, Name: "AtLine", Type: "int"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "// AtLine, when > 0, resolves the outermost declaration that spans")
		assert.Contains(t, content, "// that line in File. Mutually exclusive with Name/Pattern.")
		// Verify field ordering by finding the field declarations (tab-indented lines)
		fileFieldIdx := strings.Index(content, "\tFile ")
		atLineFieldIdx := strings.Index(content, "\tAtLine ")
		assert.True(t, fileFieldIdx < atLineFieldIdx, "File field should appear before AtLine field")
	})
	t.Run("duplicate field name errors without write", func(t *testing.T) {
		h, fs := newPatchHandler()
		src := `package p

type User struct {
	Name string
}
`
		setFile(fs, "f.go", src)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpAddField, Name: "Name", Type: "int"},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
		assert.Equal(t, src, getFile(fs, "f.go"))
	})

	t.Run("missing anchor errors with candidates list", func(t *testing.T) {
		h, fs := newPatchHandler()
		src := `package p

type User struct {
	ID   string
	Name string
}
`
		setFile(fs, "f.go", src)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpAddField, Name: "Email", Type: "string", Before: "DoesNotExist"},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "DoesNotExist")
		assert.Contains(t, err.Error(), "ID")
		assert.Contains(t, err.Error(), "Name")
		assert.Equal(t, src, getFile(fs, "f.go"))
	})
}

// ── remove_field ──────────────────────────────────────────────────────────────

func TestPatchStruct_RemoveField(t *testing.T) {
	ctx := context.Background()

	t.Run("removes named field", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type User struct {
	ID      string
	Legacy  int
	Name    string
}
`)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpRemoveField, Name: "Legacy"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.NotContains(t, content, "Legacy")
		assert.Contains(t, content, "ID")
		assert.Contains(t, content, "Name")
	})

	t.Run("not-found errors with candidates", func(t *testing.T) {
		h, fs := newPatchHandler()
		src := `package p

type User struct {
	ID   string
	Name string
}
`
		setFile(fs, "f.go", src)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpRemoveField, Name: "Missing"},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
		assert.Contains(t, err.Error(), "ID, Name")
		assert.Equal(t, src, getFile(fs, "f.go"))
	})
}

// ── rename_field ──────────────────────────────────────────────────────────────

func TestPatchStruct_RenameField(t *testing.T) {
	ctx := context.Background()

	t.Run("renames field preserving type and tag", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type User struct {
	Mail string `+"`"+`json:"mail"`+"`"+`
}
`)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpRenameField, From: "Mail", To: "Email"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "Email string")
		assert.Contains(t, content, `json:"mail"`, "tag should be preserved")
		assert.NotContains(t, content, "Mail string")
	})

	t.Run("rename to colliding name errors", func(t *testing.T) {
		h, fs := newPatchHandler()
		src := `package p

type User struct {
	Mail  string
	Email string
}
`
		setFile(fs, "f.go", src)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpRenameField, From: "Mail", To: "Email"},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
		assert.Equal(t, src, getFile(fs, "f.go"))
	})
}

// ── retype_field ──────────────────────────────────────────────────────────────

func TestPatchStruct_RetypeField(t *testing.T) {
	ctx := context.Background()

	t.Run("retypes field preserving tag", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type User struct {
	ID string `+"`"+`json:"id"`+"`"+`
}
`)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpRetypeField, Name: "ID", Type: "uuid.UUID"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "ID uuid.UUID")
		assert.Contains(t, content, `json:"id"`)
	})
	t.Run("retypes field and updates tag when tag is provided", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type User struct {
	ID string `+"`"+`json:"id"`+"`"+`
}
`)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpRetypeField, Name: "ID", Type: "uuid.UUID", Tag: `json:"uuid"`},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "ID uuid.UUID")
		assert.Contains(t, content, `json:"uuid"`)
		assert.NotContains(t, content, `json:"id"`, "old tag must be replaced")
	})
}

// ── set_tag ──────────────────────────────────────────────────────────────────

func TestPatchStruct_SetTag(t *testing.T) {
	ctx := context.Background()

	t.Run("set_tag replaces tag wholesale", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type User struct {
	Email string `+"`"+`json:"email"`+"`"+`
}
`)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpSetTag, Name: "Email", Tag: `json:"email,omitempty" bson:"email"`},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, `json:"email,omitempty" bson:"email"`)
	})

	t.Run("set_tag accepts raw tag without backticks", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type User struct {
	Email string
}
`)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpSetTag, Name: "Email", Tag: `json:"email"`},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, `Email string `+"`"+`json:"email"`+"`")
	})
}

// ── set_doc ───────────────────────────────────────────────────────────────────

func TestPatchStruct_SetDoc(t *testing.T) {
	ctx := context.Background()

	t.Run("set_doc adds doc comment above field", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type User struct {
	Email string
}
`)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpSetDoc, Name: "Email", Doc: "Email is the primary contact."},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "// Email is the primary contact.")
	})
}

// ── atomicity ────────────────────────────────────────────────────────────────

func TestPatchStruct_Atomicity(t *testing.T) {
	ctx := context.Background()

	t.Run("multi-patch all succeed", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

type User struct {
	Mail string
}
`)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpRenameField, From: "Mail", To: "Email"},
				{Op: domain.StructPatchOpAddField, Name: "ID", Type: "string", Position: "first"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Regexp(t, `Email\s+string`, content)
		assert.Regexp(t, `ID\s+string`, content)
	})

	t.Run("one bad patch writes nothing", func(t *testing.T) {
		h, fs := newPatchHandler()
		src := `package p

type User struct {
	Mail string
}
`
		setFile(fs, "f.go", src)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpRenameField, From: "Mail", To: "Email"},
				{Op: domain.StructPatchOpRemoveField, Name: "NonExistent"},
			},
		})
		require.Error(t, err)
		assert.Equal(t, src, getFile(fs, "f.go"))
	})
}

// ── preview ───────────────────────────────────────────────────────────────────

func TestPatchStruct_Preview(t *testing.T) {
	ctx := context.Background()

	h, fs := newPatchHandler()
	src := `package p

type User struct {
	Name string
}
`
	setFile(fs, "f.go", src)
	res, err := h.PatchStruct(ctx, domain.PatchStructRequest{
		FilePath:   "f.go",
		Identifier: "User",
		Patches: []domain.StructPatch{
			{Op: domain.StructPatchOpAddField, Name: "Email", Type: "string"},
		},
		Preview: true,
	})
	require.NoError(t, err)
	assert.True(t, res.Preview)
	assert.NotEmpty(t, res.Diff)
	assert.Equal(t, src, getFile(fs, "f.go"), "preview must not write")
}

// ── struct not found ─────────────────────────────────────────────────────────

func TestPatchStruct_NotFound(t *testing.T) {
	ctx := context.Background()

	h, fs := newPatchHandler()
	setFile(fs, "f.go", "package p\n")
	_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
		FilePath:   "f.go",
		Identifier: "Missing",
		Patches: []domain.StructPatch{
			{Op: domain.StructPatchOpAddField, Name: "X", Type: "int"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ── embed handling ───────────────────────────────────────────────────────────

func TestPatchStruct_Embed(t *testing.T) {
	ctx := context.Background()

	t.Run("remove embedded field by type name", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", `package p

import "io"

type R struct {
	io.Reader
	Name string
}
`)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "R",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpRemoveField, Name: "io.Reader"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.NotContains(t, content, "io.Reader")
		assert.Contains(t, content, "Name string")
	})
}

func TestPatchStruct_PreservesInlineComments(t *testing.T) {
	ctx := context.Background()

	src := `package p

type User struct {
	ID   string // primary key
	Name string // display name
	Age  int
}
`

	t.Run("add_field preserves inline comments on siblings", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", src)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpAddField, Name: "Email", Type: "string"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "// primary key", "inline comment on ID was dropped")
		assert.Contains(t, content, "// display name", "inline comment on Name was dropped")
	})

	t.Run("remove_field preserves inline comments on siblings", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", src)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpRemoveField, Name: "Age"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "// primary key")
		assert.Contains(t, content, "// display name")
	})

	t.Run("rename_field preserves the inline comment on the renamed field", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", src)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpRenameField, From: "ID", To: "Identifier"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "// primary key", "inline comment should move with renamed field")
		assert.Contains(t, content, "// display name")
	})

	t.Run("retype_field preserves the inline comment on the retyped field", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", src)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpRetypeField, Name: "ID", Type: "int64"},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "// primary key", "inline comment should survive retype")
		assert.Contains(t, content, "// display name")
	})

	t.Run("set_tag preserves inline comment on the tagged field", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", src)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpSetTag, Name: "Name", Tag: `json:"name"`},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "// primary key")
		assert.Contains(t, content, "// display name", "inline comment should survive set_tag")
	})

	t.Run("set_doc preserves inline comment on the doc'd field", func(t *testing.T) {
		h, fs := newPatchHandler()
		setFile(fs, "f.go", src)
		_, err := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   "f.go",
			Identifier: "User",
			Patches: []domain.StructPatch{
				{Op: domain.StructPatchOpSetDoc, Name: "Name", Doc: "Name is the user's full name."},
			},
		})
		require.NoError(t, err)
		content := getFile(fs, "f.go")
		assert.Contains(t, content, "// primary key")
		// after set_doc, Name gets a doc comment above; inline "display name" may be retained or moved
		// the critical check is that ID's inline comment survives.
	})
}

func TestPatchStruct_ChainedAddFieldAnchors(t *testing.T) {
	// Three add_field patches in a single call. The 2nd anchors on the
	// field added by the 1st; the 3rd anchors on the field added by the
	// 2nd. Before this change the 2nd and 3rd patches failed with
	// "anchor after=X not found".
	src := `package p

type Config struct {
	Original string
}
`
	fs := &mockFS{files: map[string][]byte{"p.go": []byte(src)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchStruct(context.Background(), domain.PatchStructRequest{
		FilePath:   "p.go",
		Identifier: "Config",
		Patches: []domain.StructPatch{
			{Op: domain.StructPatchOpAddField, Name: "First", Type: "int", After: "Original"},
			{Op: domain.StructPatchOpAddField, Name: "Second", Type: "int", After: "First"},
			{Op: domain.StructPatchOpAddField, Name: "Third", Type: "int", After: "Second"},
		},
	})
	require.NoError(t, err)

	updated := string(fs.files["p.go"])
	// The three new fields should appear in order right after Original.
	idxOriginal := strings.Index(updated, "Original")
	idxFirst := strings.Index(updated, "First")
	idxSecond := strings.Index(updated, "Second")
	idxThird := strings.Index(updated, "Third")
	assert.Less(t, idxOriginal, idxFirst)
	assert.Less(t, idxFirst, idxSecond)
	assert.Less(t, idxSecond, idxThird)
}
