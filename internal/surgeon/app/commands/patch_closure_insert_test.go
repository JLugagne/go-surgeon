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

// TestPatchFunction_ClosureIdentifier_InsertAfterAnchorsAtMatch reproduces
// backlog item 5: with identifier Parent>closure[N], resolveInsertAnchor
// received the OUTER FuncDecl while offsets are closure-relative, so the
// lift logic decided the anchor was "inside a closure", clamped the outer
// statement bounds to the closure body edges, and the insert landed at the
// BOTTOM of the closure with a bogus AutoLift instead of right after the
// matched anchor.
func TestPatchFunction_ClosureIdentifier_InsertAfterAnchorsAtMatch(t *testing.T) {
	const path = "/tmp/closure_insert_after.go"
	const content = `package p

func Parent() {
	setup()
	fn := func() {
		a := 1
		b := 2
		c := 3
	}
	fn()
}
`
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "Parent>closure[0]",
		Patches: []domain.FunctionPatch{{
			Op:    domain.PatchOpInsertAfter,
			Match: "a := 1",
			Code:  "logA()",
		}},
	})
	require.NoError(t, err)
	assert.Empty(t, res.AutoLifts, "an anchor at the closure's top level must not be auto-lifted")

	got := string(fs.files[path])
	logIdx := strings.Index(got, "logA()")
	require.GreaterOrEqual(t, logIdx, 0, "inserted code missing")
	assert.Greater(t, logIdx, strings.Index(got, "a := 1"), "insert_after must land after the anchor")
	assert.Less(t, logIdx, strings.Index(got, "b := 2"), "insert_after must land before the next statement, not at the closure bottom")
}

// TestPatchFunction_ClosureIdentifier_InsertBeforeAnchorsAtMatch is the
// insert_before side of item 5: the bogus lift clamped the start offset to 0,
// so the insert landed at the TOP of the closure body instead of immediately
// before the matched anchor.
func TestPatchFunction_ClosureIdentifier_InsertBeforeAnchorsAtMatch(t *testing.T) {
	const path = "/tmp/closure_insert_before.go"
	const content = `package p

func Parent() {
	setup()
	fn := func() {
		a := 1
		b := 2
		c := 3
	}
	fn()
}
`
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "Parent>closure[0]",
		Patches: []domain.FunctionPatch{{
			Op:    domain.PatchOpInsertBefore,
			Match: "c := 3",
			Code:  "logC()",
		}},
	})
	require.NoError(t, err)
	assert.Empty(t, res.AutoLifts, "an anchor at the closure's top level must not be auto-lifted")

	got := string(fs.files[path])
	logIdx := strings.Index(got, "logC()")
	require.GreaterOrEqual(t, logIdx, 0, "inserted code missing")
	assert.Greater(t, logIdx, strings.Index(got, "b := 2"), "insert_before must land right before the anchor, not at the closure top")
	assert.Less(t, logIdx, strings.Index(got, "c := 3"), "insert_before must land before the anchor")
}

// TestPatchFunction_ClosureIdentifier_LiftStopsAtClosureTopLevel pins the
// corrected lift semantics for drilled closures: an anchor nested inside an
// if-branch WITHIN the closure must lift to the closure's own top-level
// statement (the if), not to the outer function's statement (which the buggy
// code clamped to the top of the closure body).
func TestPatchFunction_ClosureIdentifier_LiftStopsAtClosureTopLevel(t *testing.T) {
	const path = "/tmp/closure_insert_lift.go"
	const content = `package p

func Parent() {
	setup()
	fn := func() {
		a := 1
		if cond {
			body()
		}
		c := 3
	}
	fn()
}
`
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	res, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "Parent>closure[0]",
		Patches: []domain.FunctionPatch{{
			Op:    domain.PatchOpInsertBefore,
			Match: "body()",
			Code:  "guard()",
		}},
	})
	require.NoError(t, err)
	require.Len(t, res.AutoLifts, 1, "anchor inside an if-branch of the closure must be lifted")
	assert.Contains(t, res.AutoLifts[0].LiftedFrom, "if/else branch")

	got := string(fs.files[path])
	guardIdx := strings.Index(got, "guard()")
	require.GreaterOrEqual(t, guardIdx, 0, "inserted code missing")
	assert.Greater(t, guardIdx, strings.Index(got, "a := 1"), "lift must stop at the closure's top-level if statement, not the closure top")
	assert.Less(t, guardIdx, strings.Index(got, "if cond"), "lifted insert_before must land before the enclosing if")
}

// TestPatchFunction_ClosureIdentifier_SetSignatureEditsClosureLiteral pins the
// set_signature half of item 5: on a Parent>closure[N] identifier the op must
// rewrite the closure literal's signature, not the outer function's.
func TestPatchFunction_ClosureIdentifier_SetSignatureEditsClosureLiteral(t *testing.T) {
	const path = "/tmp/closure_set_signature.go"
	const content = `package p

func Parent() {
	fn := func(x int) {
		use(x)
	}
	fn(1)
}
`
	fs := &mockFS{files: map[string][]byte{path: []byte(content)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.PatchFunction(context.Background(), domain.PatchFunctionRequest{
		FilePath:   path,
		Identifier: "Parent>closure[0]",
		Patches: []domain.FunctionPatch{{
			Op:     domain.PatchOpSetSignature,
			Params: "(x int, y int)",
		}},
	})
	require.NoError(t, err)

	got := string(fs.files[path])
	assert.Contains(t, got, "func Parent() {", "the outer function's signature must be untouched")
	assert.Contains(t, got, "func(x int, y int)", "the closure literal's signature must carry the new params")
}
