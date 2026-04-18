package mcp

import (
	"context"
	"fmt"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ── Input schemas ────────────────────────────────────────────────────────────

// patchStructBulkItemInput mirrors patchStructInput minus File/Identifier
// promoted to the per-item level so a single call can target many structs.
type patchStructBulkItemInput struct {
	File       string               `json:"file" jsonschema:"target Go file path for this item"`
	Identifier string               `json:"identifier" jsonschema:"struct name for this item, e.g. User or pkg.User"`
	Patches    []structPatchOpInput `json:"patches" jsonschema:"ordered list of patch operations to apply atomically to this struct"`
}

type patchStructBulkInput struct {
	Items   []patchStructBulkItemInput `json:"items" jsonschema:"list of (file, identifier, patches) targets; max 20"`
	Preview bool                       `json:"preview,omitempty" jsonschema:"if true, return aggregated diff without writing any file"`
}

// patchFunctionBulkItemInput mirrors patchFunctionInput minus File/Identifier
// promoted to the per-item level.
type patchFunctionBulkItemInput struct {
	File          string         `json:"file" jsonschema:"target Go file path for this item"`
	Identifier    string         `json:"identifier" jsonschema:"function or method identifier for this item, e.g. FuncName or Receiver.Method"`
	Patches       []patchOpInput `json:"patches" jsonschema:"ordered list of patch operations to apply atomically to this function"`
	IncludeNested bool           `json:"include_nested,omitempty" jsonschema:"when true, allow matches inside nested closures within this function"`
}

type patchFunctionBulkInput struct {
	Items   []patchFunctionBulkItemInput `json:"items" jsonschema:"list of (file, identifier, patches) targets; max 20"`
	Preview bool                         `json:"preview,omitempty" jsonschema:"if true, return aggregated diff without writing any file"`
}

// ── Structured output ────────────────────────────────────────────────────────

// patchBulkOutput is the structured result for patch_struct_bulk and
// patch_function_bulk. Items is parallel to the request's Items slice.
type patchBulkOutput struct {
	Items   []patchOutput `json:"items"`
	Applied int           `json:"applied"`
	Preview bool          `json:"preview,omitempty"`
	Diff    string        `json:"diff,omitempty"`
}

// ── Registration ─────────────────────────────────────────────────────────────

// registerPatchBulkTools wires patch_struct_bulk and patch_function_bulk into
// the server. Called from registerPatchTools so the bulk variants live next
// to their per-call siblings.
func registerPatchBulkTools(s *mcp.Server, commands service.SurgeonCommands) {
	registerPatchStructBulkTool(s, commands)
	registerPatchFunctionBulkTool(s, commands)
}

func registerPatchStructBulkTool(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "patch_struct_bulk",
		Description: "Apply struct field patches (add/remove/rename/retype/set_tag/set_doc) to many structs in one atomic call. Soft cap: 20 items per call; any item failure rolls back the whole batch so no file is partially written.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in patchStructBulkInput) (*mcp.CallToolResult, any, error) {
		items := make([]domain.PatchStructBulkItem, len(in.Items))
		for i, it := range in.Items {
			if err := validateGoFile(it.File); err != nil {
				return err, nil, nil
			}
			patches := make([]domain.StructPatch, len(it.Patches))
			for j, p := range it.Patches {
				patches[j] = domain.StructPatch{
					Op:       domain.StructPatchOp(p.Op),
					Name:     p.Name,
					From:     p.From,
					To:       p.To,
					Type:     p.Type,
					Tag:      p.Tag,
					Doc:      p.Doc,
					Before:   p.Before,
					After:    p.After,
					Position: p.Position,
				}
			}
			items[i] = domain.PatchStructBulkItem{
				FilePath:   it.File,
				Identifier: it.Identifier,
				Patches:    patches,
			}
		}

		result, err := commands.PatchStructBulk(ctx, domain.PatchStructBulkRequest{
			Items:   items,
			Preview: in.Preview,
		})
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (patch_struct_bulk): %v", err), err), nil, nil
		}

		out := buildStructBulkOutput(in.Items, result)
		res := textResult(renderBulkText(len(in.Items), result.Applied, result.Preview, result.Diff))
		res.StructuredContent = out
		return res, nil, nil
	})
}

func registerPatchFunctionBulkTool(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "patch_function_bulk",
		Description: "Apply function-body patches (replace/insert_before/insert_after/delete/wrap/set_signature) to many functions in one atomic call. Soft cap: 20 items per call; any item failure rolls back the whole batch so no file is partially written.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in patchFunctionBulkInput) (*mcp.CallToolResult, any, error) {
		items := make([]domain.PatchFunctionBulkItem, len(in.Items))
		for i, it := range in.Items {
			if err := validateGoFile(it.File); err != nil {
				return err, nil, nil
			}
			patches := make([]domain.FunctionPatch, len(it.Patches))
			for j, p := range it.Patches {
				patches[j] = domain.FunctionPatch{
					Op:         domain.PatchOp(p.Op),
					Match:      p.Match,
					MatchRegex: p.MatchRegex,
					Occurrence: p.Occurrence,
					Replace:    p.Replace,
					Code:       p.Code,
					Wrap:       p.Wrap,
					AtLine:     p.AtLine,
					FromLine:   p.FromLine,
					ToLine:     p.ToLine,
					Params:     p.Params,
					Returns:    p.Returns,
				}
			}
			items[i] = domain.PatchFunctionBulkItem{
				FilePath:      it.File,
				Identifier:    it.Identifier,
				Patches:       patches,
				IncludeNested: it.IncludeNested,
			}
		}

		result, err := commands.PatchFunctionBulk(ctx, domain.PatchFunctionBulkRequest{
			Items:   items,
			Preview: in.Preview,
		})
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (patch_function_bulk): %v", err), err), nil, nil
		}

		out := buildFunctionBulkOutput(in.Items, result)
		res := textResult(renderBulkText(len(in.Items), result.Applied, result.Preview, result.Diff))
		res.StructuredContent = out
		return res, nil, nil
	})
}

// ── Output builders ──────────────────────────────────────────────────────────

func buildStructBulkOutput(inputs []patchStructBulkItemInput, result domain.PatchStructBulkResult) patchBulkOutput {
	out := patchBulkOutput{
		Applied: result.Applied,
		Preview: result.Preview,
		Diff:    result.Diff,
		Items:   make([]patchOutput, len(result.Items)),
	}
	for i, r := range result.Items {
		out.Items[i] = patchOutput{
			File:         inputs[i].File,
			Identifier:   inputs[i].Identifier,
			Applied:      r.Applied,
			Preview:      r.Preview,
			Diff:         r.Diff,
			AddedImports: r.AddedImports,
			Warnings:     r.Warnings,
		}
	}
	return out
}

func buildFunctionBulkOutput(inputs []patchFunctionBulkItemInput, result domain.PatchFunctionBulkResult) patchBulkOutput {
	out := patchBulkOutput{
		Applied: result.Applied,
		Preview: result.Preview,
		Diff:    result.Diff,
		Items:   make([]patchOutput, len(result.Items)),
	}
	for i, r := range result.Items {
		var liftJSON []autoLiftJSON
		for _, al := range r.AutoLifts {
			liftJSON = append(liftJSON, autoLiftJSON{
				PatchIndex: al.PatchIndex,
				LiftedFrom: al.LiftedFrom,
				LiftedTo:   al.LiftedTo,
				Context:    al.Context,
			})
		}
		out.Items[i] = patchOutput{
			File:         inputs[i].File,
			Identifier:   inputs[i].Identifier,
			Applied:      r.Applied,
			Preview:      r.Preview,
			Diff:         r.Diff,
			AddedImports: r.AddedImports,
			Warnings:     r.Warnings,
			AutoLifts:    liftJSON,
		}
	}
	return out
}

// renderBulkText builds the human-readable tool-result text: an "OK: N/M
// items applied" (or "PREVIEW:") header followed by the aggregated diff
// (which already carries per-item "--- file:identifier ---" separators).
func renderBulkText(itemCount, applied int, preview bool, diff string) string {
	header := fmt.Sprintf("OK: %d/%d items applied", itemCount, itemCount)
	if preview {
		header = fmt.Sprintf("PREVIEW: %d/%d items (not written)", itemCount, itemCount)
	}
	// When applied == 0 every item's patch list was empty (or a no-op).
	// Surface that so the caller does not assume success by default.
	if applied == 0 && !preview {
		header = fmt.Sprintf("OK: 0/%d items applied (all no-op)", itemCount)
	}
	if diff == "" {
		return header
	}
	return header + "\n\n" + diff
}
