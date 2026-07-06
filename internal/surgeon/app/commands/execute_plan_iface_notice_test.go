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

// TestExecutePlan_UpdateInterfaceAppendNoticeSurfaced asserts that when an
// update_interface action falls back to appending a new declaration (the
// identifier was not found), the "not found, appended" notice reaches the
// plan result as a warning. Before the fix, executeAction discarded the
// interface handler's first return value, silently swallowing the notice
// while the plan reported plain success.
func TestExecutePlan_UpdateInterfaceAppendNoticeSurfaced(t *testing.T) {
	const path = "/tmp/exec_plan_iface_notice.go"
	const original = "package svc\n\ntype Real interface {\n\tDo()\n}\n"
	fs := &mockFS{files: map[string][]byte{path: []byte(original)}}
	h := commands.NewExecutePlanHandler(fs)

	result, err := h.ExecutePlan(context.Background(), domain.Plan{
		Actions: []domain.Action{{
			Action:     domain.ActionTypeUpdateInterface,
			FilePath:   path,
			Identifier: "Missing",
			Content:    "type Missing interface {\n\tGo()\n}",
		}},
	})
	require.NoError(t, err)

	require.NotEmpty(t, result.Warnings, "append fallback must surface a plan warning")
	joined := strings.Join(result.Warnings, "\n")
	assert.Contains(t, joined, "not found", "warning must explain the identifier was not found")

	// The content was still appended (fallback behavior is preserved).
	assert.Contains(t, string(fs.files[path]), "Missing interface")
}
