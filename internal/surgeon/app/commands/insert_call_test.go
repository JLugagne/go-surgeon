package commands_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeInsertCallPlan(file, id, call string, pos domain.InsertPosition) domain.Plan {
	return domain.Plan{Actions: []domain.Action{{
		Action:     domain.ActionTypeInsertCall,
		FilePath:   file,
		Identifier: id,
		Content:    call,
		Position:   pos,
	}}}
}

func TestInsertCall_BeforeReturn(t *testing.T) {
	const src = `package wire

func wireOrder(mux *http.ServeMux, app *app.App) {
	setupCreateOrderRoute(mux, app)
	return
}
`
	fs := &mockFS{files: map[string][]byte{"wire.go": []byte(src)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.ExecutePlan(context.Background(), makeInsertCallPlan(
		"wire.go", "wireOrder", "setupPayOrderRoute(mux, app)", domain.InsertBeforeReturn,
	))
	require.NoError(t, err)

	out := string(fs.files["wire.go"])
	assert.Contains(t, out, "setupPayOrderRoute(mux, app)")
	// Must appear before the return.
	payIdx := indexOf(out, "setupPayOrderRoute")
	retIdx := indexOf(out, "return")
	assert.Less(t, payIdx, retIdx, "setupPayOrderRoute must appear before return")
	// Existing line preserved.
	assert.Contains(t, out, "setupCreateOrderRoute(mux, app)")
}

func TestInsertCall_EndOfBody(t *testing.T) {
	const src = `package wire

func wireOrder(mux *http.ServeMux, app *app.App) {
	setupCreateOrderRoute(mux, app)
}
`
	fs := &mockFS{files: map[string][]byte{"wire.go": []byte(src)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.ExecutePlan(context.Background(), makeInsertCallPlan(
		"wire.go", "wireOrder", "setupPayOrderRoute(mux, app)", domain.InsertEndOfBody,
	))
	require.NoError(t, err)

	out := string(fs.files["wire.go"])
	assert.Contains(t, out, "setupPayOrderRoute(mux, app)")
	// setupPay must come after setupCreate.
	createIdx := indexOf(out, "setupCreateOrderRoute")
	payIdx := indexOf(out, "setupPayOrderRoute")
	assert.Greater(t, payIdx, createIdx)
}

func TestInsertCall_AfterMarker(t *testing.T) {
	const src = `package wire

func wireOrder(mux *http.ServeMux, app *app.App) {
	// order routes
	setupCreateOrderRoute(mux, app)
}
`
	fs := &mockFS{files: map[string][]byte{"wire.go": []byte(src)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.ExecutePlan(context.Background(), makeInsertCallPlan(
		"wire.go", "wireOrder", "setupPayOrderRoute(mux, app)", "after:// order routes",
	))
	require.NoError(t, err)

	out := string(fs.files["wire.go"])
	assert.Contains(t, out, "setupPayOrderRoute(mux, app)")
	// Must appear right after the marker comment line.
	markerIdx := indexOf(out, "// order routes")
	payIdx := indexOf(out, "setupPayOrderRoute")
	createIdx := indexOf(out, "setupCreateOrderRoute")
	assert.Greater(t, payIdx, markerIdx)
	assert.Less(t, payIdx, createIdx, "inserted call should appear before the pre-existing call")
}

func TestInsertCall_Idempotent(t *testing.T) {
	const src = `package wire

func wireOrder(mux *http.ServeMux, app *app.App) {
	setupPayOrderRoute(mux, app)
	return
}
`
	fs := &mockFS{files: map[string][]byte{"wire.go": []byte(src)}}
	h := commands.NewExecutePlanHandler(fs)

	result, err := h.ExecutePlan(context.Background(), makeInsertCallPlan(
		"wire.go", "wireOrder", "setupPayOrderRoute(mux, app)", domain.InsertBeforeReturn,
	))
	require.NoError(t, err)
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "already present")

	// File must not be modified.
	assert.Equal(t, src, string(fs.files["wire.go"]))
}

func TestInsertCall_FunctionNotFound(t *testing.T) {
	const src = `package wire

func wireOrder(mux *http.ServeMux, app *app.App) {
	return
}
`
	fs := &mockFS{files: map[string][]byte{"wire.go": []byte(src)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.ExecutePlan(context.Background(), makeInsertCallPlan(
		"wire.go", "nonExistentFunc", "someCall()", domain.InsertBeforeReturn,
	))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NODE_NOT_FOUND")
}

func TestInsertCall_MarkerNotFound(t *testing.T) {
	const src = `package wire

func wireOrder(mux *http.ServeMux, app *app.App) {
	setupCreateOrderRoute(mux, app)
}
`
	fs := &mockFS{files: map[string][]byte{"wire.go": []byte(src)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.ExecutePlan(context.Background(), makeInsertCallPlan(
		"wire.go", "wireOrder", "setupPayOrderRoute(mux, app)", "after:// missing marker",
	))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MARKER_NOT_FOUND")
}

func TestInsertCall_InvalidPosition(t *testing.T) {
	const src = `package wire

func wireOrder(mux *http.ServeMux, app *app.App) {}
`
	fs := &mockFS{files: map[string][]byte{"wire.go": []byte(src)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.ExecutePlan(context.Background(), makeInsertCallPlan(
		"wire.go", "wireOrder", "someCall()", "bad-position",
	))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "INVALID_POSITION")
}

func TestInsertCall_DefaultPositionIsBeforeReturn(t *testing.T) {
	const src = `package wire

func wireOrder(mux *http.ServeMux, app *app.App) {
	return
}
`
	fs := &mockFS{files: map[string][]byte{"wire.go": []byte(src)}}
	h := commands.NewExecutePlanHandler(fs)

	// Empty position → defaults to before-return.
	_, err := h.ExecutePlan(context.Background(), makeInsertCallPlan(
		"wire.go", "wireOrder", "setupPayOrderRoute(mux, app)", "",
	))
	require.NoError(t, err)

	out := string(fs.files["wire.go"])
	payIdx := indexOf(out, "setupPayOrderRoute")
	retIdx := indexOf(out, "return")
	assert.Less(t, payIdx, retIdx)
}

func TestInsertCall_NoReturnFallsToEndOfBody(t *testing.T) {
	// Function with no return statement — before-return should insert at end of body.
	const src = `package wire

func wireOrder(mux *http.ServeMux, app *app.App) {
	setupCreateOrderRoute(mux, app)
}
`
	fs := &mockFS{files: map[string][]byte{"wire.go": []byte(src)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.ExecutePlan(context.Background(), makeInsertCallPlan(
		"wire.go", "wireOrder", "setupPayOrderRoute(mux, app)", domain.InsertBeforeReturn,
	))
	require.NoError(t, err)

	out := string(fs.files["wire.go"])
	assert.Contains(t, out, "setupPayOrderRoute(mux, app)")
}

func TestInsertCall_Method(t *testing.T) {
	const src = `package wire

type Wirer struct{}

func (w *Wirer) Setup(mux *http.ServeMux, app *app.App) {
	return
}
`
	fs := &mockFS{files: map[string][]byte{"wire.go": []byte(src)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.ExecutePlan(context.Background(), makeInsertCallPlan(
		"wire.go", "Wirer.Setup", "setupPayOrderRoute(mux, app)", domain.InsertBeforeReturn,
	))
	require.NoError(t, err)

	out := string(fs.files["wire.go"])
	assert.Contains(t, out, "setupPayOrderRoute(mux, app)")
}

func TestInsertCall_AutoLift_MarkerInClosure(t *testing.T) {
	// The marker text "registerTools()" lives inside a func-literal
	// closure. insert_call with after:<marker> should NOT land inside
	// the closure body — it should auto-lift to the top-level
	// statement that owns the closure.
	const src = `package wire

func NewServer() {
	setupStart()
	once(func() {
		registerTools()
	})
	setupEnd()
}

func setupStart()     {}
func setupEnd()       {}
func registerTools()  {}
func once(fn func())  { fn() }
`
	fs := &mockFS{files: map[string][]byte{"wire.go": []byte(src)}}
	h := commands.NewExecutePlanHandler(fs)

	_, err := h.ExecutePlan(context.Background(), makeInsertCallPlan(
		"wire.go", "NewServer", "registerMoreTools()", "after:registerTools()",
	))
	require.NoError(t, err)

	out := string(fs.files["wire.go"])
	// registerMoreTools must appear after the once(...) block (top-level)
	// and before setupEnd() — not inside the closure.
	ixOnce := indexOf(out, "once(")
	ixMore := indexOf(out, "registerMoreTools()")
	ixEnd := indexOf(out, "setupEnd()")
	require.GreaterOrEqual(t, ixMore, 0, "inserted call not found")
	assert.Greater(t, ixMore, ixOnce, "inserted call should appear after the once(...) block")
	assert.Less(t, ixMore, ixEnd, "inserted call should appear before setupEnd()")
	// Top-level single-tab indentation.
	assert.Regexp(t, `(?m)^\tregisterMoreTools\(\)$`, out)
}

// indexOf returns the byte index of substr in s, or -1.
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
