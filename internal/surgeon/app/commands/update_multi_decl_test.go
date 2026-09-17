package commands_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpdateStruct_RejectsMultipleDeclarations reproduces issue #35:
// update object=struct with content declaring several top-level types
// silently mis-splices the extra declarations. It must be rejected and
// leave the file untouched.
func TestUpdateStruct_RejectsMultipleDeclarations(t *testing.T) {
	filePath := "/virtual/update_multi_struct.go"
	initial := "package p\n\ntype A struct{ X int }\n\ntype B struct{ Y int }\n"
	fs := &mockFS{files: map[string][]byte{filePath: []byte(initial)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.ExecutePlan(context.Background(), domain.Plan{Actions: []domain.Action{{
		Action:     domain.ActionTypeUpdateStruct,
		FilePath:   filePath,
		Identifier: "A",
		Content:    "type A struct{ X int }\n\ntype B struct{ Y int; Z string }",
	}}})
	require.Error(t, err, "multi-declaration struct content must be rejected")
	var de *domain.Error
	require.ErrorAs(t, err, &de)
	assert.Equal(t, "INVALID_ARGUMENT", de.Code)
	assert.Equal(t, initial, string(fs.files[filePath]), "file must be untouched")
}

// TestUpdateFunc_RejectsMultipleDeclarations is the func half of #35.
func TestUpdateFunc_RejectsMultipleDeclarations(t *testing.T) {
	filePath := "/virtual/update_multi_func.go"
	initial := "package p\n\nfunc A() {}\n\nfunc B() {}\n"
	fs := &mockFS{files: map[string][]byte{filePath: []byte(initial)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.ExecutePlan(context.Background(), domain.Plan{Actions: []domain.Action{{
		Action:     domain.ActionTypeUpdateFunc,
		FilePath:   filePath,
		Identifier: "A",
		Content:    "func A() {}\n\nfunc B() { println(\"b\") }",
	}}})
	require.Error(t, err, "multi-declaration func content must be rejected")
	var de *domain.Error
	require.ErrorAs(t, err, &de)
	assert.Equal(t, "INVALID_ARGUMENT", de.Code)
	assert.Equal(t, initial, string(fs.files[filePath]), "file must be untouched")
}

// TestUpdateStruct_SingleDeclarationStillWorks guards against
// over-restricting the fix.
func TestUpdateStruct_SingleDeclarationStillWorks(t *testing.T) {
	filePath := "/virtual/update_single_struct.go"
	initial := "package p\n\ntype A struct{ X int }\n"
	fs := &mockFS{files: map[string][]byte{filePath: []byte(initial)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.ExecutePlan(context.Background(), domain.Plan{Actions: []domain.Action{{
		Action:     domain.ActionTypeUpdateStruct,
		FilePath:   filePath,
		Identifier: "A",
		Content:    "type A struct{ X int; Y string }",
	}}})
	require.NoError(t, err)
	assert.Contains(t, string(fs.files[filePath]), "Y string")
}

// TestAddStruct_MultipleDeclarationsStillAllowed documents that the
// single-declaration rule applies to update only (the documented
// add-struct example adds a type plus a const block).
func TestAddStruct_MultipleDeclarationsStillAllowed(t *testing.T) {
	filePath := "/virtual/add_multi_struct.go"
	initial := "package p\n"
	fs := &mockFS{files: map[string][]byte{filePath: []byte(initial)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.ExecutePlan(context.Background(), domain.Plan{Actions: []domain.Action{{
		Action:   domain.ActionTypeAddStruct,
		FilePath: filePath,
		Content:  "type Status string\n\nconst StatusDraft Status = \"draft\"",
	}}})
	require.NoError(t, err)
	got := string(fs.files[filePath])
	assert.Contains(t, got, "type Status string")
	assert.Contains(t, got, "const StatusDraft")
}
