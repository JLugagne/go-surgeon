package mcp_test

import (
	"context"
	"testing"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecutePlan_FilePatchParity_TabIndentedSingleLine is the regression
// guard for item 34: a single-line, tab-indented literal match reported zero
// matches through execute_plan patch_file_ops while the identical match
// applied fine through the standalone patch tool. It captures the
// domain.FilePatch that each MCP path hands to the command layer and asserts
// they are byte-for-byte identical — proving the MCP conversion/plumbing is
// not the source of the divergence (any remaining anomaly would live in the
// app/commands plan pipeline, outside this layer).
func TestExecutePlan_FilePatchParity_TabIndentedSingleLine(t *testing.T) {
	const match = "\tif verbose {"
	const replace = "\tif cfg.Verbose {"

	var standalone domain.FilePatch
	var viaPlan domain.FilePatch

	commands := &mockCommands{
		patchFileFn: func(_ context.Context, req domain.PatchFileRequest) (domain.PatchFileResult, error) {
			require.Len(t, req.Patches, 1)
			standalone = req.Patches[0]
			return domain.PatchFileResult{Applied: 1, Diff: "d", Hits: []int{1}}, nil
		},
		executePlanFn: func(_ context.Context, plan domain.Plan) (domain.PlanResult, error) {
			require.Len(t, plan.Actions, 1)
			require.Len(t, plan.Actions[0].PatchFileOps, 1)
			viaPlan = plan.Actions[0].PatchFileOps[0]
			return domain.PlanResult{FilesModified: 1}, nil
		},
	}
	cs := setupTest(t, commands, &mockQueries{})

	standaloneRes := callTool(t, cs, "patch", map[string]any{
		"target": "file",
		"file":   "verbose.go",
		"patches": []map[string]any{
			{"match": match, "replace": replace},
		},
	})
	require.False(t, standaloneRes.IsError, resultText(t, standaloneRes))

	planRes := callTool(t, cs, "execute_plan", map[string]any{
		"actions": []map[string]any{
			{
				"action":         "patch_file",
				"file":           "verbose.go",
				"patch_file_ops": []map[string]any{{"match": match, "replace": replace}},
			},
		},
	})
	require.False(t, planRes.IsError, resultText(t, planRes))

	assert.Equal(t, match, standalone.Match, "standalone must preserve tab-indented match verbatim")
	assert.Equal(t, match, viaPlan.Match, "execute_plan must preserve tab-indented match verbatim")
	assert.Equal(t, standalone, viaPlan, "both MCP paths must hand the command layer an identical FilePatch")
}
