package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
)

// patchFunctionBulkMaxItems is the soft cap on the number of items items[] in a
// patch target=function call may contain. It keeps error messages manageable
// and discourages callers from batching together unrelated work.
const patchFunctionBulkMaxItems = 20

// PatchFunctionBulk applies the same kind of per-item function-body patches
// to many (file, identifier, patches) targets atomically. Semantics mirror
// PatchStructBulk:
//
//   - If any item fails the entire call fails and no file on disk is
//     modified. The returned error names the offending item's 1-based index.
//   - Preview mode returns the aggregated unified diff across all items.
//   - Non-preview mode first runs a dry-run through PreviewWith to verify
//     every item applies cleanly; on success it re-runs against the real
//     filesystem to write.
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

	items := make([]domain.PatchFunctionResult, len(req.Items))
	diff, _, err := h.PreviewWith(ctx, func(sc service.SurgeonCommands) error {
		for i, it := range req.Items {
			r, perr := sc.PatchFunction(ctx, domain.PatchFunctionRequest{
				FilePath:      it.FilePath,
				Identifier:    it.Identifier,
				Patches:       it.Patches,
				Preview:       false,
				IncludeNested: it.IncludeNested,
			})
			if perr != nil {
				return &domain.Error{
					Code:    domainErrorCode(perr),
					Message: fmt.Sprintf("patch (target=function): item #%d (%s:%s) failed: %v", i+1, it.FilePath, it.Identifier, perr),
					Err:     perr,
				}
			}
			items[i] = r
		}
		return nil
	})
	if err != nil {
		return domain.PatchFunctionBulkResult{}, err
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

	realItems := make([]domain.PatchFunctionResult, len(req.Items))
	for i, it := range req.Items {
		r, perr := h.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:      it.FilePath,
			Identifier:    it.Identifier,
			Patches:       it.Patches,
			Preview:       false,
			IncludeNested: it.IncludeNested,
		})
		if perr != nil {
			return domain.PatchFunctionBulkResult{}, &domain.Error{
				Code:    domainErrorCode(perr),
				Message: fmt.Sprintf("patch (target=function): item #%d (%s:%s) failed during write phase: %v", i+1, it.FilePath, it.Identifier, perr),
				Err:     perr,
			}
		}
		realItems[i] = r
	}

	return domain.PatchFunctionBulkResult{
		Items:   realItems,
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
