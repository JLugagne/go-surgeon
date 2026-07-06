package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// patchFunctionBulkMaxItems is the soft cap on the number of items items[] in a
// patch target=function call may contain. It keeps error messages manageable
// and discourages callers from batching together unrelated work.
const patchFunctionBulkMaxItems = 20

// PatchFunctionBulk applies the same kind of per-item function-body patches
// to many (file, identifier, patches) targets atomically. Semantics mirror
// PatchStructBulk:
//
//   - Every item is resolved and applied against a single in-memory overlay,
//     so same-file items compose and each item sees the previous items'
//     writes exactly as they will be committed.
//   - If any item fails the entire call fails and no file on disk is modified.
//     The returned error names the offending item's 1-based index.
//   - Preview mode returns the aggregated unified diff across all items.
//   - Non-preview mode commits the overlay once (running goimports per file)
//     only after every item resolved cleanly. Resolving all items before any
//     goimports pass avoids the earlier phase-2 divergence where a real write
//     of one item shifted line numbers and broke a later same-file at_line
//     item.
func (h *ExecutePlanHandler) PatchFunctionBulk(ctx context.Context, req domain.PatchFunctionBulkRequest) (domain.PatchFunctionBulkResult, error) {
	if len(req.Items) > patchFunctionBulkMaxItems {
		return domain.PatchFunctionBulkResult{}, &domain.Error{
			Code:    "INVALID_ARGUMENT",
			Message: fmt.Sprintf("patch (target=function): max %d items per call in items[], got %d", patchFunctionBulkMaxItems, len(req.Items)),
		}
	}
	if len(req.Items) == 0 {
		return domain.PatchFunctionBulkResult{}, &domain.Error{
			Code:    "INVALID_ARGUMENT",
			Message: "patch (target=function): items[] requires at least one entry",
		}
	}

	previewH, overlay := h.previewHandler()
	items := make([]domain.PatchFunctionResult, len(req.Items))
	for i, it := range req.Items {
		// Force Preview=false so the write lands in the overlay buffer and
		// later items on the same file see the updated content.
		r, perr := previewH.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:      it.FilePath,
			Identifier:    it.Identifier,
			Patches:       it.Patches,
			Preview:       false,
			IncludeNested: it.IncludeNested,
		})
		if perr != nil {
			return domain.PatchFunctionBulkResult{}, &domain.Error{
				Code:    domainErrorCode(perr),
				Message: fmt.Sprintf("patch (target=function): item #%d (%s:%s) failed: %v", i+1, it.FilePath, it.Identifier, perr),
				Err:     perr,
			}
		}
		items[i] = r
	}

	applied := 0
	for _, it := range items {
		applied += it.Applied
	}

	if req.Preview {
		return domain.PatchFunctionBulkResult{
			Items:   items,
			Applied: applied,
			Preview: true,
			Diff:    buildFunctionBulkDiff(req.Items, items),
		}, nil
	}

	diff, err := overlay.Diff(ctx)
	if err != nil {
		return domain.PatchFunctionBulkResult{}, err
	}
	if _, err := h.commitOverlay(ctx, overlay, domain.PlanResult{}); err != nil {
		return domain.PatchFunctionBulkResult{}, err
	}

	return domain.PatchFunctionBulkResult{
		Items:   items,
		Applied: applied,
		Preview: false,
		Diff:    diff,
	}, nil
}

// buildFunctionBulkDiff joins per-item function diffs with a header
// separator. Items whose diff is empty (no-op) are skipped so the output
// stays compact.
func buildFunctionBulkDiff(inputs []domain.PatchFunctionBulkItem, results []domain.PatchFunctionResult) string {
	var parts []string
	for i, r := range results {
		if r.Diff == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("--- %s:%s ---\n%s", inputs[i].FilePath, inputs[i].Identifier, r.Diff))
	}
	return strings.Join(parts, "\n")
}
