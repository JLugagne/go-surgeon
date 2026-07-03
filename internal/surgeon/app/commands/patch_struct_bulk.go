package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// patchStructBulkMaxItems is the soft cap on the number of items items[] in a
// patch target=struct call may contain. It keeps error messages manageable and
// discourages callers from batching together unrelated work.
const patchStructBulkMaxItems = 20

// PatchStructBulk applies the same kind of per-item struct patches to many
// (file, identifier, patches) targets atomically. Semantics:
//
//   - Every item is resolved and applied against a single in-memory overlay,
//     so same-file items compose and each item sees the previous items'
//     writes exactly as they will be committed.
//   - If any item fails (parse error, missing identifier, bad patch), the
//     entire call fails and no file on disk is modified. The returned error
//     names the offending item's 1-based index so the caller knows where to
//     retry.
//   - In preview mode (req.Preview=true) the aggregated unified diff of every
//     item is returned without writing.
//   - In non-preview mode the overlay is committed once (running goimports
//     per file) only after every item resolved cleanly — a single commit,
//     rather than a second per-item pass over goimports-transformed disk
//     content, so what is committed matches what was resolved.
func (h *ExecutePlanHandler) PatchStructBulk(ctx context.Context, req domain.PatchStructBulkRequest) (domain.PatchStructBulkResult, error) {
	if len(req.Items) > patchStructBulkMaxItems {
		return domain.PatchStructBulkResult{}, &domain.Error{
			Code:    "INVALID_ARGUMENT",
			Message: fmt.Sprintf("patch (target=struct): max %d items per call in items[], got %d", patchStructBulkMaxItems, len(req.Items)),
		}
	}
	if len(req.Items) == 0 {
		return domain.PatchStructBulkResult{}, &domain.Error{
			Code:    "INVALID_ARGUMENT",
			Message: "patch (target=struct): items[] requires at least one entry",
		}
	}

	previewH, overlay := h.previewHandler()
	items := make([]domain.PatchStructResult, len(req.Items))
	for i, it := range req.Items {
		// Force Preview=false so the write lands in the overlay buffer and
		// later items on the same file see the updated content.
		r, perr := previewH.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   it.FilePath,
			Identifier: it.Identifier,
			Patches:    it.Patches,
			Preview:    false,
		})
		if perr != nil {
			return domain.PatchStructBulkResult{}, &domain.Error{
				Code:    domainErrorCode(perr),
				Message: fmt.Sprintf("patch (target=struct): item #%d (%s:%s) failed: %v", i+1, it.FilePath, it.Identifier, perr),
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
		return domain.PatchStructBulkResult{
			Items:   items,
			Applied: applied,
			Preview: true,
			Diff:    buildStructBulkDiff(req.Items, items),
		}, nil
	}

	diff, err := overlay.Diff(ctx)
	if err != nil {
		return domain.PatchStructBulkResult{}, err
	}
	if _, err := h.commitOverlay(ctx, overlay, domain.PlanResult{}); err != nil {
		return domain.PatchStructBulkResult{}, err
	}

	return domain.PatchStructBulkResult{
		Items:   items,
		Applied: applied,
		Preview: false,
		Diff:    diff,
	}, nil
}

// buildStructBulkDiff joins per-item struct diffs with a header separator.
// Items whose diff is empty (no-op) are skipped so the output stays compact.
func buildStructBulkDiff(inputs []domain.PatchStructBulkItem, results []domain.PatchStructResult) string {
	var parts []string
	for i, r := range results {
		if r.Diff == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("--- %s:%s ---\n%s", inputs[i].FilePath, inputs[i].Identifier, r.Diff))
	}
	return strings.Join(parts, "\n")
}

// domainErrorCode extracts the Code from a *domain.Error; falls back to
// "PATCH_FAILED" for plain errors so the caller always gets a stable tag.
func domainErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var de *domain.Error
	if errors.As(err, &de) {
		return de.Code
	}
	return "PATCH_FAILED"
}
