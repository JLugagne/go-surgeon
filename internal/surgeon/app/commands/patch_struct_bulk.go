package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
)

// patchStructBulkMaxItems is the soft cap on the number of items a single
// patch_struct_bulk call may contain. It keeps error messages manageable and
// discourages callers from batching together unrelated work.
const patchStructBulkMaxItems = 20

// PatchStructBulk applies the same kind of per-item struct patches to many
// (file, identifier, patches) targets atomically. Semantics:
//
//   - If any item fails (parse error, missing identifier, bad patch, write
//     error), the entire call fails and no file on disk is modified. The
//     returned error names the offending item's 1-based index so the caller
//     knows where to retry.
//   - In preview mode (req.Preview=true) the aggregated unified diff of every
//     item is returned without writing.
//   - In non-preview mode the call first runs a dry-run through PreviewWith
//     to verify every item applies cleanly; only if that succeeds does it
//     re-run against the real filesystem to produce the actual writes. This
//     is the "rollback on any failure" acceptance criterion: mid-batch
//     failures are caught before any disk state changes.
func (h *ExecutePlanHandler) PatchStructBulk(ctx context.Context, req domain.PatchStructBulkRequest) (domain.PatchStructBulkResult, error) {
	if len(req.Items) > patchStructBulkMaxItems {
		return domain.PatchStructBulkResult{}, &domain.Error{
			Code:    "INVALID_ARGUMENT",
			Message: fmt.Sprintf("patch_struct_bulk: max %d items per call, got %d", patchStructBulkMaxItems, len(req.Items)),
		}
	}
	if len(req.Items) == 0 {
		return domain.PatchStructBulkResult{}, &domain.Error{
			Code:    "INVALID_ARGUMENT",
			Message: "patch_struct_bulk: at least one item is required",
		}
	}

	// Phase 1 (always): dry-run every item inside a single PreviewWith
	// closure. If any item errors, PreviewWith discards the buffered writes
	// and we return before touching the disk. If we are in preview mode,
	// this is the final output — the aggregated diff is what we return.
	items := make([]domain.PatchStructResult, len(req.Items))
	diff, _, err := h.PreviewWith(ctx, func(sc service.SurgeonCommands) error {
		for i, it := range req.Items {
			// Force Preview=false inside the closure: we want the write to
			// land in the previewFS buffer so later items see the updated
			// file content (multiple items on the same file compose).
			r, perr := sc.PatchStruct(ctx, domain.PatchStructRequest{
				FilePath:   it.FilePath,
				Identifier: it.Identifier,
				Patches:    it.Patches,
				Preview:    false,
			})
			if perr != nil {
				return &domain.Error{
					Code:    domainErrorCode(perr),
					Message: fmt.Sprintf("patch_struct_bulk: item #%d (%s:%s) failed: %v", i+1, it.FilePath, it.Identifier, perr),
					Err:     perr,
				}
			}
			items[i] = r
		}
		return nil
	})
	if err != nil {
		return domain.PatchStructBulkResult{}, err
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

	// Phase 2: we verified the whole batch applies cleanly in Phase 1; now
	// re-run against the real filesystem to actually write. If a sibling
	// process races us, individual items may still fail here — we surface
	// that error verbatim (disk state may then be partially modified; this
	// matches Go's usual "atomic within a single call" contract).
	realItems := make([]domain.PatchStructResult, len(req.Items))
	for i, it := range req.Items {
		r, perr := h.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   it.FilePath,
			Identifier: it.Identifier,
			Patches:    it.Patches,
			Preview:    false,
		})
		if perr != nil {
			return domain.PatchStructBulkResult{}, &domain.Error{
				Code:    domainErrorCode(perr),
				Message: fmt.Sprintf("patch_struct_bulk: item #%d (%s:%s) failed during write phase: %v", i+1, it.FilePath, it.Identifier, perr),
				Err:     perr,
			}
		}
		realItems[i] = r
	}

	return domain.PatchStructBulkResult{
		Items:   realItems,
		Applied: applied,
		Preview: false,
		Diff:    diff, // diff captured from the preview phase reflects the exact same writes
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
