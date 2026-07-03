package commands_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const readerIfaceSrc = "package repo\n\n// Reader is the read port.\ntype Reader interface {\n\tRead(id string) error\n}\n\nfunc Keep() {}\n"

// TestUpdateInterface_DocOnlyKeepsDeclaration asserts a doc-only update
// (the MCP interface tool explicitly allows content to be omitted when doc
// is set) rewrites the doc comment WITHOUT deleting the interface. Before
// the fix, empty content was spliced over the whole declaration, silently
// replacing the interface with just the comment.
func TestUpdateInterface_DocOnlyKeepsDeclaration(t *testing.T) {
	const path = "/tmp/iface_doc_only.go"
	fs := &mockFS{files: map[string][]byte{path: []byte(readerIfaceSrc)}}
	h := commands.NewExecutePlanHandler(fs)

	_, _, err := h.UpdateInterface(context.Background(), domain.InterfaceActionRequest{
		FilePath:   path,
		Identifier: "Reader",
		Doc:        "Reader reads aggregates by id.",
	})
	require.NoError(t, err)

	updated := string(fs.files[path])
	assert.Contains(t, updated, "// Reader reads aggregates by id.", "new doc must be set")
	assert.Contains(t, updated, "type Reader interface", "the interface declaration must survive a doc-only update")
	assert.Contains(t, updated, "Read(id string) error", "the method set must survive")
}

// TestUpdateInterface_StripDocKeepsDeclaration covers the strip_doc-only
// variant of the same bug (the declaration vanished entirely).
func TestUpdateInterface_StripDocKeepsDeclaration(t *testing.T) {
	const path = "/tmp/iface_strip_doc.go"
	fs := &mockFS{files: map[string][]byte{path: []byte(readerIfaceSrc)}}
	h := commands.NewExecutePlanHandler(fs)

	_, _, err := h.UpdateInterface(context.Background(), domain.InterfaceActionRequest{
		FilePath:   path,
		Identifier: "Reader",
		StripDoc:   true,
	})
	require.NoError(t, err)

	updated := string(fs.files[path])
	assert.NotContains(t, updated, "// Reader is the read port.", "old doc must be stripped")
	assert.Contains(t, updated, "type Reader interface", "the interface declaration must survive strip_doc")
}
