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

// TestPatchFunction_InsertAtBodyStart_DoesNotWeldSignatureLine reproduces
// issue #38: an insert_before that lands at the very start of the target
// body (here an explicitly targeted closure, Parent>closure[0]) was
// spliced immediately after the opening brace, welding the inserted
// statement onto the closure's signature line:
//
//	fn := func() error { for i := range workspaces {
//
// The enclosing declaration's signature line must stay untouched.
func TestPatchFunction_InsertAtBodyStart_DoesNotWeldSignatureLine(t *testing.T) {
	const path = "/virtual/weld.go"
	const content = "package p\n\nfunc createCmd(workspaces []string) {\n" +
		"\tfn := func() error { if cond { create := CreateRequest{Name: workspaces[0]}; _ = create }; return nil }\n" +
		"\t_ = fn\n}\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:      path,
		Identifier:    "createCmd>closure[0]",
		IncludeNested: true,
		Patches: []domain.FunctionPatch{{
			Op:    domain.PatchOpInsertBefore,
			Match: "create := CreateRequest{",
			Code:  "for i := range workspaces {\n\tworkspaces[i] = Expand(workspaces[i])\n}",
		}},
	})
	require.NoError(t, err)

	got := string(fs.files[path])
	assert.Contains(t, got, "func() error {\n", "the closure signature line must end at the opening brace")
	assert.NotContains(t, got, "error {for i", "inserted code must not weld onto the signature line")
	assert.NotContains(t, got, "error { for i", "inserted code must not weld onto the signature line")
	require.Contains(t, got, "for i := range workspaces {", "inserted code missing")
	assert.Less(t, strings.Index(got, "for i := range workspaces {"), strings.Index(got, "create := CreateRequest{"),
		"inserted code must precede the anchor")
}
