package commands_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecutePlan_FailedActionRollsBackEarlierWrites asserts the documented
// contract "any failure rolls everything back": when a later action fails,
// files touched by earlier actions must be untouched. Before the fix the
// plan wrote sequentially straight to disk, so the first edit persisted.
func TestExecutePlan_FailedActionRollsBackEarlierWrites(t *testing.T) {
	const path = "/tmp/atomic_plan.go"
	const original = "package pkg\n\nfunc Greet() string {\n\treturn \"hi\"\n}\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(original)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.ExecutePlan(context.Background(), domain.Plan{
		Actions: []domain.Action{
			{
				Action:     domain.ActionTypeUpdateFunc,
				FilePath:   path,
				Identifier: "Greet",
				Content:    "func Greet() string {\n\treturn \"hello\"\n}",
			},
			{
				Action:     domain.ActionTypePatchFunction,
				FilePath:   path,
				Identifier: "DoesNotExist",
				PatchFunctionOps: []domain.FunctionPatch{{
					Op:      domain.PatchOpReplace,
					Match:   "nothing",
					Replace: "something",
				}},
			},
		},
	})
	require.Error(t, err, "the second action must fail")
	assert.Equal(t, original, string(fs.files[path]), "a failed plan must leave earlier actions unwritten")
}

// TestExecutePlan_ActionLimitEnforced asserts the documented 15-action cap
// is enforced (domain.ErrActionLimitExceeded existed but was never used).
func TestExecutePlan_ActionLimitEnforced(t *testing.T) {
	fs := &mockFS{files: map[string][]byte{}}
	h := commands.NewExecutePlanHandler(fs)

	actions := make([]domain.Action, 16)
	for i := range actions {
		actions[i] = domain.Action{
			Action:   domain.ActionTypeCreateFile,
			FilePath: fmt.Sprintf("/tmp/limit_%d.go", i),
			Content:  "func F() {}\n",
		}
	}
	_, err := h.ExecutePlan(context.Background(), domain.Plan{Actions: actions})
	require.Error(t, err, "16 actions must exceed the documented 15-action cap")
	assert.Empty(t, fs.files, "no file may be written when the plan is rejected")
}
