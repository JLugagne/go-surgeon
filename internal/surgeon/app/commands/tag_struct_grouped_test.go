package commands_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Backlog item 29: TagStruct only looked at Names[0], so targeting a
// non-first member of a grouped field (X, Y int) silently did nothing.
func TestTagStruct_SetTagOnGroupedMemberDoesNotNoOp(t *testing.T) {
	fs := &mockFS{files: map[string][]byte{"point.go": []byte(`package p

type Point struct {
	X, Y int
}
`)}}
	h := commands.NewExecutePlanHandler(fs)

	err := h.TagStruct(context.Background(), domain.TagRequest{
		FilePath:   "point.go",
		StructName: "Point",
		FieldName:  "Y",
		SetTag:     `json:"y"`,
	})
	require.NoError(t, err)

	content := string(fs.files["point.go"])
	fields := snapshotStructFields(t, content, "Point")
	assert.Equal(t, `json:"y"`, fields["Y"].tag,
		"tagging Y in a grouped field must not be a silent no-op:\n%s", content)
	assert.Empty(t, fields["X"].tag,
		"the untargeted group member must stay untagged:\n%s", content)
	assert.Equal(t, "int", fields["X"].typeExpr)
	assert.Equal(t, "int", fields["Y"].typeExpr)
}

// Backlog item 29: auto-tagging a struct with a grouped field derived the
// tag from Names[0] and attached it to the whole declaration, giving every
// member the same (duplicate) key.
func TestTagStruct_AutoTagGroupedFieldTagsEachName(t *testing.T) {
	fs := &mockFS{files: map[string][]byte{"point.go": []byte(`package p

type Point struct {
	X, Y int
}
`)}}
	h := commands.NewExecutePlanHandler(fs)

	err := h.TagStruct(context.Background(), domain.TagRequest{
		FilePath:   "point.go",
		StructName: "Point",
		AutoFormat: "json",
	})
	require.NoError(t, err)

	content := string(fs.files["point.go"])
	fields := snapshotStructFields(t, content, "Point")
	assert.Equal(t, `json:"x"`, fields["X"].tag, "X must get its own key:\n%s", content)
	assert.Equal(t, `json:"y"`, fields["Y"].tag,
		"Y must get its own key, not a duplicate of X's:\n%s", content)
}
