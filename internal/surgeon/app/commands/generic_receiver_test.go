package commands_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const genericStoreSrc = "package store\n\ntype Store[T any] struct{ items []T }\n\nfunc (s *Store[T]) Get(i int) T {\n\treturn s.items[i]\n}\n"

// TestPatchFunction_GenericReceiver asserts Receiver.Method identifiers
// resolve for methods on generic types — getRecvType must unwrap the
// IndexExpr receiver (Store[T]) instead of returning \"\".
func TestPatchFunction_GenericReceiver(t *testing.T) {
	const path = "/tmp/generic_store.go"
	fs := &mockFS{files: map[string][]byte{path: []byte(genericStoreSrc)}}
	h := commands.NewExecutePlanHandler(fs)

	result, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "Store.Get",
		Patches: []domain.FunctionPatch{{
			Op:      domain.PatchOpReplace,
			Match:   "return s.items[i]",
			Replace: "return s.items[i%len(s.items)]",
		}},
	})
	require.NoError(t, err, "generic receiver method must be patchable")
	assert.Equal(t, 1, result.Applied)
	assert.Contains(t, string(fs.files[path]), "i%len(s.items)")
}

// TestDeleteStruct_GenericTypeRemovesMethods asserts delete_struct on a
// generic type also removes its methods; leaving them orphans the receiver
// type and breaks the build with a reported SUCCESS.
func TestDeleteStruct_GenericTypeRemovesMethods(t *testing.T) {
	const path = "/tmp/generic_delete.go"
	src := genericStoreSrc + "\nfunc Keep() {}\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(src)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.ExecutePlan(context.Background(), domain.Plan{
		Actions: []domain.Action{{Action: domain.ActionTypeDeleteStruct, FilePath: path, Identifier: "Store"}},
	})
	require.NoError(t, err)

	updated := string(fs.files[path])
	assert.NotContains(t, updated, "type Store", "struct must be deleted")
	assert.NotContains(t, updated, "func (s *Store[T]) Get", "methods of the deleted generic struct must be removed too")
	assert.Contains(t, updated, "func Keep()", "unrelated declarations must survive")
}
