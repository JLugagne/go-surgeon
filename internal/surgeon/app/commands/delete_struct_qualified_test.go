package commands_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeleteStruct_QualifiedIdentifierRemovesMethods asserts that deleting a
// struct via a package-qualified identifier (pkg.Name) also removes its
// methods. The struct is resolved through parseIdentifier, but method
// receivers were compared against the raw "store.User" string, so the bare
// receiver "User" never matched and methods survived the deletion.
func TestDeleteStruct_QualifiedIdentifierRemovesMethods(t *testing.T) {
	const path = "/tmp/delete_struct_qualified.go"
	original := "package store\n\n" +
		"type User struct {\n\tID int\n}\n\n" +
		"func (u *User) Name() string {\n\treturn \"x\"\n}\n\n" +
		"func Keep() {}\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(original)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.ExecutePlan(context.Background(), domain.Plan{
		Actions: []domain.Action{{
			Action:     domain.ActionTypeDeleteStruct,
			FilePath:   path,
			Identifier: "store.User",
		}},
	})
	require.NoError(t, err)

	updated := string(fs.files[path])
	assert.NotContains(t, updated, "type User struct", "the struct itself must be removed")
	assert.NotContains(t, updated, "func (u *User) Name()", "methods of the deleted struct must be removed")
	assert.Contains(t, updated, "func Keep()", "unrelated declarations must survive")
}
