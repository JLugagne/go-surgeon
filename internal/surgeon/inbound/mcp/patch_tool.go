package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// patchInput is the unified input for the merged patch tool. The patch tool
// always takes a list of items: length 1 for single-target edits, length N
// for batch edits.
//
//   - target=function and target=struct: items are dispatched as one atomic
//     bulk call to the domain layer (commands.PatchFunctionBulk /
//     commands.PatchStructBulk). Any item failure rolls back the whole batch.
//   - target=interface, target=file and target=decl: items are applied
//     sequentially using the per-item domain command. There is NO atomicity
//     across items for these targets — if item K fails, items 0..K-1 remain
//     written and the call returns the error from item K.
type patchInput struct {
	Target  string           `json:"target" jsonschema:"which declaration to patch: function, struct, interface, file, or decl"`
	Items   []patchItemInput `json:"items" jsonschema:"list of (file, identifier, patches) targets to patch in this call; length 1 for single-target, length N for batch. function/struct items are atomic across the batch; interface/file/decl items are applied sequentially (an early failure leaves earlier items written)."`
	Preview bool             `json:"preview,omitempty" jsonschema:"if true, return diff without writing any file"`
}

// patchItemInput is one (file, identifier, patches) target inside the items
// array. Most fields are target-specific:
//
//   - File:       always required.
//   - Identifier: required for function/struct/interface/decl; ignored for file.
//   - Patches:    target-specific shape (see patchToolDescription).
//   - IncludeNested: function-only; allow matches inside nested closures.
//   - MockFile / MockName: interface-only; regenerate the mock when the method set changes.
//   - Scope:      file-only; "all" (default), "code_only", or "identifiers_only".
type patchItemInput struct {
	File          string           `json:"file" jsonschema:"target Go file path"`
	Identifier    string           `json:"identifier,omitempty" jsonschema:"declaration name; required for function/struct/interface/decl, ignored for file"`
	Patches       []map[string]any `json:"patches" jsonschema:"ordered list of patch operations; shape depends on target — see tool description"`
	IncludeNested bool             `json:"include_nested,omitempty" jsonschema:"function only: also match inside nested closures (default: top-level body only)"`
	MockFile      string           `json:"mock_file,omitempty" jsonschema:"interface only: regenerate this mock file when the method set changes"`
	MockName      string           `json:"mock_name,omitempty" jsonschema:"interface only: name of the mock struct to regenerate"`
	Scope         string           `json:"scope,omitempty" jsonschema:"file only: all (default), code_only, or identifiers_only"`
}

// patchBulkOutput is the structured result for the unified patch tool. Items
// is parallel to the request's Items slice. For single-item calls Items has
// length 1.
type patchBulkOutput struct {
	Items   []patchOutput `json:"items"`
	Applied int           `json:"applied"`
	Preview bool          `json:"preview,omitempty"`
	Diff    string        `json:"diff,omitempty"`
}

const patchToolDescription = "Surgical AST-aware editor — one tool for all declaration kinds. " +
	"ALWAYS takes items: [{file, identifier, patches, ...}]; length 1 for a single edit, length N for a batch. " +
	"Set target to select what kind of declaration each item edits: " +
	"'function' edits lines inside a func/method body; " +
	"'struct' edits a struct's field list; " +
	"'interface' edits an interface's method list (and regenerates the mock when mock_file+mock_name are set); " +
	"'file' does whole-file text substitution for cross-function batch edits; " +
	"'decl' edits a top-level const/var value. " +
	"BATCH SEMANTICS: function and struct items are atomic across the batch (any failure rolls everything back). " +
	"interface, file and decl items are applied sequentially — if item K fails, items before it remain written. " +
	"All targets: each item's file + patches required; preview=true returns diff without writing. " +
	"FUNCTION ops: replace, insert_before, insert_after, delete, wrap, set_signature. " +
	"SIGNATURE: set_signature takes params (array of declarations without parens, e.g. [\"ctx context.Context\", \"x int\"]) and/or returns; at least one is required. " +
	"LINE TARGETING (preferred for function/decl): at_line or from_line/to_line with file-absolute line numbers — faster and unambiguous than text match. " +
	"TEXT MATCHING (fallback): match (whitespace-normalized) or match_regex (RE2); disambiguate with occurrence. " +
	"STRUCT ops: add_field, remove_field, rename_field, retype_field, set_tag, set_doc. " +
	"DOCS: doc on add_field/set_doc accepts multiline text using \\n. " +
	"INTERFACE ops: add_method, remove_method, rename_method, retype_method, set_doc, embed, remove_embed. " +
	"FILE patches apply sequentially within an item; scope: all (default), code_only, identifiers_only. " +
	"DECL targets the value expression of a named const/var; string literal delimiters are preserved automatically. WHEN TO USE update INSTEAD: op=replace is still a weak spot for multi-line replacements — for replacements that span multiple lines, contain tabs/escapes, or restructure a large struct literal, prefer 'update object=func' (or update object=struct/file) with the full new declaration. patch validates op=replace results post-splice (issues #3 and #14): replacements whose substring is missing or whose declarations were silently dropped are refused with PATCH_REPLACE_NOT_APPLIED / PATCH_DROPPED_CONTENT and the file is left unchanged. Call 'describe_tool name=patch' for the full Limitations list."

func registerPatchTool(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "patch",
		Description: patchToolDescription,
	}, func(ctx context.Context, req *mcp.CallToolRequest, in patchInput) (*mcp.CallToolResult, any, error) {
		if len(in.Items) == 0 {
			return errorResult("items is required: provide at least one {file, identifier, patches} target"), nil, nil
		}
		for _, it := range in.Items {
			if err := validateGoFile(it.File); err != nil {
				return err, nil, nil
			}
		}
		switch in.Target {
		case "function":
			return handlePatchFunction(ctx, commands, in)
		case "struct":
			return handlePatchStruct(ctx, commands, in)
		case "interface":
			return handlePatchInterface(ctx, commands, in)
		case "file":
			return handlePatchFile(ctx, commands, in)
		case "decl":
			return handlePatchDecl(ctx, commands, in)
		case "":
			return errorResult("target is required: function, struct, interface, file, or decl"), nil, nil
		default:
			return errorResult(fmt.Sprintf("unknown target %q — must be one of: function, struct, interface, file, decl", in.Target)), nil, nil
		}
	})
}

// decodeFunctionOps decodes one item's raw patches into typed function patch
// ops. Returns an error description on failure.
func decodeFunctionOps(raw []map[string]any) ([]patchOpInput, error) {
	var ops []patchOpInput
	buf, _ := json.Marshal(raw)
	if err := json.Unmarshal(buf, &ops); err != nil {
		return nil, err
	}
	return ops, nil
}

func decodeStructOps(raw []map[string]any) ([]structPatchOpInput, error) {
	var ops []structPatchOpInput
	buf, _ := json.Marshal(raw)
	if err := json.Unmarshal(buf, &ops); err != nil {
		return nil, err
	}
	return ops, nil
}

func decodeInterfaceOps(raw []map[string]any) ([]interfacePatchOpInput, error) {
	var ops []interfacePatchOpInput
	buf, _ := json.Marshal(raw)
	if err := json.Unmarshal(buf, &ops); err != nil {
		return nil, err
	}
	return ops, nil
}

func decodeFileOps(raw []map[string]any) ([]filePatchOpInput, error) {
	var ops []filePatchOpInput
	buf, _ := json.Marshal(raw)
	if err := json.Unmarshal(buf, &ops); err != nil {
		return nil, err
	}
	return ops, nil
}

func handlePatchFunction(ctx context.Context, commands service.SurgeonCommands, in patchInput) (*mcp.CallToolResult, any, error) {
	// Decode every item's ops up front; surface any decode error.
	itemOps := make([][]patchOpInput, len(in.Items))
	bulkItems := make([]domain.PatchFunctionBulkItem, len(in.Items))
	for i, it := range in.Items {
		ops, err := decodeFunctionOps(it.Patches)
		if err != nil {
			return errorResult(fmt.Sprintf("items[%d].patches must be an array of function patch ops: %v", i, err)), nil, nil
		}
		itemOps[i] = ops
		patches := make([]domain.FunctionPatch, len(ops))
		for j, p := range ops {
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
				Params:     joinParams(p.Params),
				Returns:    p.Returns,
			}
		}
		bulkItems[i] = domain.PatchFunctionBulkItem{
			FilePath:      it.File,
			Identifier:    it.Identifier,
			Patches:       patches,
			IncludeNested: it.IncludeNested,
		}
	}

	// Single-item path preserves the historical text shape (banner, hint,
	// AUTO_LIFTED detail lines) that agents rely on.
	if len(in.Items) == 1 {
		it := in.Items[0]
		ops := itemOps[0]
		result, err := commands.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:      it.File,
			Identifier:    it.Identifier,
			Patches:       bulkItems[0].Patches,
			Preview:       in.Preview,
			IncludeNested: it.IncludeNested,
		})
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (patch function): %v", err), err), nil, nil
		}
		return renderSingleFunction(it, ops, result), nil, nil
	}

	result, err := commands.PatchFunctionBulk(ctx, domain.PatchFunctionBulkRequest{
		Items:   bulkItems,
		Preview: in.Preview,
	})
	if err != nil {
		return errorResultWithCode(fmt.Sprintf("ERROR (patch function bulk): %v", err), err), nil, nil
	}
	out := buildFunctionBulkOutput(in.Items, result)
	res := textResult(renderBulkText(len(in.Items), result.Applied, result.Preview, result.Diff))
	res.StructuredContent = out
	return res, nil, nil
}

// renderSingleFunction emits the legacy single-item function-patch text
// (banner + hint + diff) and a length-1 patchBulkOutput for the structured
// content.
func renderSingleFunction(it patchItemInput, ops []patchOpInput, result domain.PatchFunctionResult) *mcp.CallToolResult {
	prefix := fmt.Sprintf("OK: %d patch(es) applied", result.Applied)
	if result.Preview {
		prefix = fmt.Sprintf("PREVIEW: %d patch(es) (not written)", result.Applied)
	}
	if len(result.AddedImports) > 0 {
		prefix += fmt.Sprintf(" (imports added: %s)", strings.Join(result.AddedImports, ", "))
	}
	for _, w := range result.Warnings {
		prefix += "\n  WARNING: " + w
	}
	hint := replaceShorterHint(ops)
	if hint != "" {
		prefix += "\n  HINT: " + hint
	}
	var liftJSON []autoLiftJSON
	for _, al := range result.AutoLifts {
		liftJSON = append(liftJSON, autoLiftJSON{
			PatchIndex: al.PatchIndex,
			LiftedFrom: al.LiftedFrom,
			LiftedTo:   al.LiftedTo,
			Context:    al.Context,
		})
		prefix += fmt.Sprintf("\n  AUTO_LIFTED patch #%d: from %s -> %s", al.PatchIndex, al.LiftedFrom, al.LiftedTo)
		if al.Context != "" {
			prefix += "\n" + al.Context
		}
	}
	if len(result.AutoLifts) > 0 {
		prefix = fmt.Sprintf("⚠ AUTO-LIFTED: %d patch(es) moved to the enclosing top-level statement\n\n", len(result.AutoLifts)) + prefix
	}
	item := patchOutput{
		File: it.File, Identifier: it.Identifier,
		Applied: result.Applied, Preview: result.Preview,
		Diff: result.Diff, AddedImports: result.AddedImports,
		Warnings: result.Warnings, Hint: hint, AutoLifts: liftJSON,
	}
	out := patchBulkOutput{Items: []patchOutput{item}, Applied: result.Applied, Preview: result.Preview, Diff: result.Diff}
	var res *mcp.CallToolResult
	if result.Diff != "" {
		res = textResult(prefix + "\n\n" + result.Diff)
	} else {
		res = textResult(prefix)
	}
	res.StructuredContent = out
	return res
}

func handlePatchStruct(ctx context.Context, commands service.SurgeonCommands, in patchInput) (*mcp.CallToolResult, any, error) {
	bulkItems := make([]domain.PatchStructBulkItem, len(in.Items))
	for i, it := range in.Items {
		ops, err := decodeStructOps(it.Patches)
		if err != nil {
			return errorResult(fmt.Sprintf("items[%d].patches must be an array of struct patch ops: %v", i, err)), nil, nil
		}
		patches := make([]domain.StructPatch, len(ops))
		for j, p := range ops {
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
		bulkItems[i] = domain.PatchStructBulkItem{
			FilePath:   it.File,
			Identifier: it.Identifier,
			Patches:    patches,
		}
	}

	if len(in.Items) == 1 {
		it := in.Items[0]
		result, err := commands.PatchStruct(ctx, domain.PatchStructRequest{
			FilePath:   it.File,
			Identifier: it.Identifier,
			Patches:    bulkItems[0].Patches,
			Preview:    in.Preview,
		})
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (patch struct): %v", err), err), nil, nil
		}
		return renderSingleStruct(it, result), nil, nil
	}

	result, err := commands.PatchStructBulk(ctx, domain.PatchStructBulkRequest{
		Items:   bulkItems,
		Preview: in.Preview,
	})
	if err != nil {
		return errorResultWithCode(fmt.Sprintf("ERROR (patch struct bulk): %v", err), err), nil, nil
	}
	out := buildStructBulkOutput(in.Items, result)
	res := textResult(renderBulkText(len(in.Items), result.Applied, result.Preview, result.Diff))
	res.StructuredContent = out
	return res, nil, nil
}

func renderSingleStruct(it patchItemInput, result domain.PatchStructResult) *mcp.CallToolResult {
	prefix := fmt.Sprintf("OK: %d patch(es) applied", result.Applied)
	if result.Preview {
		prefix = fmt.Sprintf("PREVIEW: %d patch(es) (not written)", result.Applied)
	}
	if len(result.AddedImports) > 0 {
		prefix += fmt.Sprintf(" (imports added: %s)", strings.Join(result.AddedImports, ", "))
	}
	for _, w := range result.Warnings {
		prefix += "\n  WARNING: " + w
	}
	item := patchOutput{
		File: it.File, Identifier: it.Identifier,
		Applied: result.Applied, Preview: result.Preview,
		Diff: result.Diff, AddedImports: result.AddedImports,
		Warnings: result.Warnings,
	}
	out := patchBulkOutput{Items: []patchOutput{item}, Applied: result.Applied, Preview: result.Preview, Diff: result.Diff}
	var res *mcp.CallToolResult
	if result.Diff != "" {
		res = textResult(prefix + "\n\n" + result.Diff)
	} else {
		res = textResult(prefix)
	}
	res.StructuredContent = out
	return res
}

func handlePatchInterface(ctx context.Context, commands service.SurgeonCommands, in patchInput) (*mcp.CallToolResult, any, error) {
	// No domain-level bulk for interface — apply sequentially.
	out := patchBulkOutput{Preview: in.Preview, Items: make([]patchOutput, 0, len(in.Items))}
	var diffs []string
	for i, it := range in.Items {
		ops, err := decodeInterfaceOps(it.Patches)
		if err != nil {
			return errorResult(fmt.Sprintf("items[%d].patches must be an array of interface patch ops: %v", i, err)), nil, nil
		}
		patches := make([]domain.InterfacePatch, len(ops))
		for j, p := range ops {
			patches[j] = domain.InterfacePatch{
				Op:        domain.InterfacePatchOp(p.Op),
				Name:      p.Name,
				From:      p.From,
				To:        p.To,
				Signature: p.Signature,
				Type:      p.Type,
				Doc:       p.Doc,
				Before:    p.Before,
				After:    p.After,
				Position:  p.Position,
			}
		}
		result, err := commands.PatchInterface(ctx, domain.PatchInterfaceRequest{
			FilePath:   it.File,
			Identifier: it.Identifier,
			Patches:    patches,
			Preview:    in.Preview,
			MockFile:   it.MockFile,
			MockName:   it.MockName,
		})
		if err != nil {
			// items 0..i-1 already wrote (when not preview); surface the error
			// so the caller knows the batch is partially applied.
			return errorResultWithCode(fmt.Sprintf("ERROR (patch interface item %d): %v", i, err), err), nil, nil
		}
		out.Items = append(out.Items, patchOutput{
			File: it.File, Identifier: it.Identifier,
			Applied: result.Applied, Preview: result.Preview,
			Diff: result.Diff, MockUpdated: result.MockUpdated,
			AddedImports: result.AddedImports, Warnings: result.Warnings,
		})
		out.Applied += result.Applied
		if result.Diff != "" {
			diffs = append(diffs, fmt.Sprintf("--- %s:%s ---\n%s", it.File, it.Identifier, result.Diff))
		}
	}
	out.Diff = strings.Join(diffs, "\n")

	if len(in.Items) == 1 {
		// Preserve the single-item text shape (used to be handlePatchInterface).
		it := in.Items[0]
		item := out.Items[0]
		prefix := fmt.Sprintf("OK: %d patch(es) applied", item.Applied)
		if item.Preview {
			prefix = fmt.Sprintf("PREVIEW: %d patch(es) (not written)", item.Applied)
		}
		if item.MockUpdated {
			prefix += " (mock regenerated)"
		}
		if len(item.AddedImports) > 0 {
			prefix += fmt.Sprintf(" (imports added: %s)", strings.Join(item.AddedImports, ", "))
		}
		for _, w := range item.Warnings {
			prefix += "\n  WARNING: " + w
		}
		// Single-item: top-level diff matches the inner one without a header.
		out.Diff = item.Diff
		var res *mcp.CallToolResult
		if item.Diff != "" {
			res = textResult(prefix + "\n\n" + item.Diff)
		} else {
			res = textResult(prefix)
		}
		_ = it
		res.StructuredContent = out
		return res, nil, nil
	}
	res := textResult(renderBulkText(len(in.Items), out.Applied, in.Preview, out.Diff))
	res.StructuredContent = out
	return res, nil, nil
}

func handlePatchFile(ctx context.Context, commands service.SurgeonCommands, in patchInput) (*mcp.CallToolResult, any, error) {
	// No domain-level bulk for file — apply sequentially.
	type fileItemView struct {
		out  patchOutput
		ops  []filePatchOpInput
		hits []int
	}
	views := make([]fileItemView, 0, len(in.Items))
	var diffs []string
	totalApplied := 0
	for i, it := range in.Items {
		ops, err := decodeFileOps(it.Patches)
		if err != nil {
			return errorResult(fmt.Sprintf("items[%d].patches must be an array of file patch ops: %v", i, err)), nil, nil
		}
		patches := make([]domain.FilePatch, len(ops))
		for j, p := range ops {
			patches[j] = domain.FilePatch{
				Match:        p.Match,
				MatchRegex:   p.MatchRegex,
				Replace:      p.Replace,
				MatchLiteral: p.MatchLiteral,
				Occurrence:   p.Occurrence,
				MatchMode:    p.MatchMode,
			}
		}
		result, err := commands.PatchFile(ctx, domain.PatchFileRequest{
			FilePath: it.File,
			Patches:  patches,
			Preview:  in.Preview,
			Scope:    it.Scope,
		})
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (patch file item %d): %v", i, err), err), nil, nil
		}
		hint := replaceShorterHintFile(ops)
		views = append(views, fileItemView{
			out: patchOutput{
				File: it.File, Identifier: "",
				Applied: result.Applied, Preview: result.Preview,
				Diff: result.Diff, AddedImports: result.AddedImports,
				Warnings: result.Warnings, Hint: hint,
			},
			ops:  ops,
			hits: result.Hits,
		})
		totalApplied += result.Applied
		if result.Diff != "" {
			diffs = append(diffs, fmt.Sprintf("--- %s ---\n%s", it.File, result.Diff))
		}
	}

	out := patchBulkOutput{Preview: in.Preview, Applied: totalApplied}
	out.Items = make([]patchOutput, len(views))
	for i, v := range views {
		out.Items[i] = v.out
	}

	if len(in.Items) == 1 {
		it := in.Items[0]
		v := views[0]
		prefix := fmt.Sprintf("OK: %d patch(es) applied", v.out.Applied)
		if v.out.Preview {
			prefix = fmt.Sprintf("PREVIEW: %d patch(es) (not written)", v.out.Applied)
		}
		if len(v.hits) > 0 {
			hitStrs := make([]string, len(v.hits))
			for i, h := range v.hits {
				hitStrs[i] = fmt.Sprintf("#%d=%d", i+1, h)
			}
			prefix += fmt.Sprintf(" [hits %s]", strings.Join(hitStrs, ", "))
		}
		if len(v.out.AddedImports) > 0 {
			prefix += fmt.Sprintf(" (imports added: %s)", strings.Join(v.out.AddedImports, ", "))
		}
		for _, w := range v.out.Warnings {
			prefix += "\n  WARNING: " + w
		}
		if v.out.Hint != "" {
			prefix += "\n  HINT: " + v.out.Hint
		}
		// Use the single-item structured shape: a length-1 patchBulkOutput
		// with patchFileOutput-shaped item is good enough; tests read items[0].
		// Diff is the item's diff (no per-file header) for backwards compat.
		out.Diff = v.out.Diff
		var res *mcp.CallToolResult
		if v.out.Diff != "" {
			res = textResult(prefix + "\n\n" + v.out.Diff)
		} else {
			res = textResult(prefix)
		}
		_ = it
		// For target=file we expose a patchFileOutput-equivalent at items[0]
		// so existing top-level fields (file/applied/hits/diff/...) still
		// have a clear home. Hits is a per-item array; persist it on the
		// item via a small extension type below.
		out.Items[0] = patchOutput{
			File:         v.out.File,
			Applied:      v.out.Applied,
			Preview:      v.out.Preview,
			Diff:         v.out.Diff,
			AddedImports: v.out.AddedImports,
			Warnings:     v.out.Warnings,
			Hint:         v.out.Hint,
		}
		res.StructuredContent = out
		return res, nil, nil
	}
	out.Diff = strings.Join(diffs, "\n")
	res := textResult(renderBulkText(len(in.Items), totalApplied, in.Preview, out.Diff))
	res.StructuredContent = out
	return res, nil, nil
}

func handlePatchDecl(ctx context.Context, commands service.SurgeonCommands, in patchInput) (*mcp.CallToolResult, any, error) {
	out := patchBulkOutput{Preview: in.Preview}
	out.Items = make([]patchOutput, 0, len(in.Items))
	var diffs []string
	for i, it := range in.Items {
		ops, err := decodeFunctionOps(it.Patches)
		if err != nil {
			return errorResult(fmt.Sprintf("items[%d].patches must be an array of decl patch ops: %v", i, err)), nil, nil
		}
		patches := make([]domain.FunctionPatch, len(ops))
		for j, p := range ops {
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
			}
		}
		result, err := commands.PatchDecl(ctx, domain.PatchDeclRequest{
			FilePath:   it.File,
			Identifier: it.Identifier,
			Patches:    patches,
			Preview:    in.Preview,
		})
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (patch decl item %d): %v", i, err), err), nil, nil
		}
		hint := replaceShorterHint(ops)
		out.Items = append(out.Items, patchOutput{
			File: it.File, Identifier: it.Identifier,
			Applied: result.Applied, Preview: result.Preview,
			Diff: result.Diff, AddedImports: result.AddedImports,
			Warnings: result.Warnings, Hint: hint,
		})
		out.Applied += result.Applied
		if result.Diff != "" {
			diffs = append(diffs, fmt.Sprintf("--- %s:%s ---\n%s", it.File, it.Identifier, result.Diff))
		}
	}

	if len(in.Items) == 1 {
		it := in.Items[0]
		item := out.Items[0]
		prefix := fmt.Sprintf("OK: %d patch(es) applied", item.Applied)
		if item.Preview {
			prefix = fmt.Sprintf("PREVIEW: %d patch(es) (not written)", item.Applied)
		}
		if len(item.AddedImports) > 0 {
			prefix += fmt.Sprintf(" (imports added: %s)", strings.Join(item.AddedImports, ", "))
		}
		for _, w := range item.Warnings {
			prefix += "\n  WARNING: " + w
		}
		if item.Hint != "" {
			prefix += "\n  HINT: " + item.Hint
		}
		out.Diff = item.Diff
		var res *mcp.CallToolResult
		if item.Diff != "" {
			res = textResult(prefix + "\n\n" + item.Diff)
		} else {
			res = textResult(prefix)
		}
		_ = it
		res.StructuredContent = out
		return res, nil, nil
	}
	out.Diff = strings.Join(diffs, "\n")
	res := textResult(renderBulkText(len(in.Items), out.Applied, in.Preview, out.Diff))
	res.StructuredContent = out
	return res, nil, nil
}

// ── Bulk helpers (atomic targets: function, struct) ─────────────────────────

// renderBulkText builds the human-readable tool-result text: an "OK: N/M
// items applied" (or "PREVIEW:") header followed by the aggregated diff
// (which already carries per-item "--- file:identifier ---" separators).
func renderBulkText(itemCount, applied int, preview bool, diff string) string {
	header := fmt.Sprintf("OK: %d/%d items applied", itemCount, itemCount)
	if preview {
		header = fmt.Sprintf("PREVIEW: %d/%d items (not written)", itemCount, itemCount)
	}
	if applied == 0 && !preview {
		header = fmt.Sprintf("OK: 0/%d items applied (all no-op)", itemCount)
	}
	if diff == "" {
		return header
	}
	return header + "\n\n" + diff
}

func buildStructBulkOutput(inputs []patchItemInput, result domain.PatchStructBulkResult) patchBulkOutput {
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

func buildFunctionBulkOutput(inputs []patchItemInput, result domain.PatchFunctionBulkResult) patchBulkOutput {
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

// replaceShorterHint scans the supplied patches for op=replace ops whose
// replacement text is shorter than the matched text. When at least one such
// op is present, it returns the agent-facing hint string that points to
// 'update object=func' as the recommended workaround for the multi-line
// replacement edge case tracked by issue #3. Returns "" when no shrinking
// replace is detected.
func replaceShorterHint(ops []patchOpInput) string {
	for _, p := range ops {
		if p.Op != "replace" {
			continue
		}
		if p.Match == "" {
			continue
		}
		if len(p.Replace) < len(p.Match) {
			return "replacement applied but result is shorter than input — try update object=func for whole-declaration rewrites (see describe_tool name=patch for the full Limitations list)"
		}
	}
	return ""
}

// replaceShorterHintFile is the patch_file (whole-file substitution)
// counterpart of replaceShorterHint. It inspects the file-level patches and
// flags the same shrinking-replace pattern that motivates the update fallback.
func replaceShorterHintFile(ops []filePatchOpInput) string {
	for _, p := range ops {
		if p.Match == "" {
			continue
		}
		if len(p.Replace) < len(p.Match) {
			return "replacement applied but result is shorter than input — try update object=func for whole-declaration rewrites (see describe_tool name=patch for the full Limitations list)"
		}
	}
	return ""
}
