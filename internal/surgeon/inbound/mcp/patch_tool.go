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

// patchInput is the unified input for the merged patch tool.
// target selects which declaration kind to edit; patches is decoded
// per-target into the appropriate op slice.
type patchInput struct {
	Target string `json:"target" jsonschema:"which declaration to patch: function, struct, interface, file, or decl"`
	File   string `json:"file" jsonschema:"target Go file path"`
	// Identifier is the declaration name. Required for function/struct/interface/decl;
	// not used for file (whole-file substitution).
	Identifier string `json:"identifier,omitempty" jsonschema:"declaration name: FuncName, Receiver.Method, StructName, InterfaceName, or const/var name"`
	// Patches is target-dependent:
	//   function/decl: [{op, match?, match_regex?, occurrence?, replace?, code?, wrap?, at_line?, from_line?, to_line?, params?, returns?}]
	//   struct:        [{op, name?, from?, to?, type?, tag?, doc?, before?, after?, position?}]
	//   interface:     [{op, name?, from?, to?, signature?, type?, doc?, before?, after?, position?}]
	//   file:          [{match?, match_regex?, replace}]
	Patches []map[string]any `json:"patches" jsonschema:"ordered list of patch operations; shape depends on target — see description"`
	Preview bool             `json:"preview,omitempty" jsonschema:"if true, return diff without writing the file"`
	// function-only
	IncludeNested bool `json:"include_nested,omitempty" jsonschema:"function only: also match inside nested closures (default: top-level body only)"`
	// interface-only
	MockFile string `json:"mock_file,omitempty" jsonschema:"interface only: regenerate this mock file when the method set changes"`
	MockName string `json:"mock_name,omitempty" jsonschema:"interface only: name of the mock struct to regenerate"`
	// file-only
	Scope string `json:"scope,omitempty" jsonschema:"file only: all (default), code_only, or identifiers_only"`
}

const patchToolDescription = "Surgical AST-aware editor — one tool for all declaration kinds. " +
	"Set target to select what to edit: " +
	"'function' edits lines inside a func/method body; " +
	"'struct' edits a struct's field list; " +
	"'interface' edits an interface's method list (and regenerates the mock when mock_file+mock_name are set); " +
	"'file' does whole-file text substitution for cross-function batch edits; " +
	"'decl' edits a top-level const/var value. " +
	"All targets: file + patches required; preview=true returns diff without writing. " +
	"FUNCTION ops: replace, insert_before, insert_after, delete, wrap, set_signature. " +
	"SIGNATURE: set_signature takes params (array of declarations without parens, e.g. [\"ctx context.Context\", \"x int\"]) and/or returns; at least one is required. " +
	"LINE TARGETING (preferred for function/decl): at_line or from_line/to_line with file-absolute line numbers — faster and unambiguous than text match. " +
	"TEXT MATCHING (fallback): match (whitespace-normalized) or match_regex (RE2); disambiguate with occurrence. " +
	"STRUCT ops: add_field, remove_field, rename_field, retype_field, set_tag, set_doc. " +
	"DOCS: doc on add_field/set_doc accepts multiline text using \\n. " +
	"INTERFACE ops: add_method, remove_method, rename_method, retype_method, set_doc, embed, remove_embed. " +
	"FILE patches apply sequentially; scope: all (default), code_only, identifiers_only. " +
	"DECL targets the value expression of a named const/var; string literal delimiters are preserved automatically."

func registerPatchTool(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "patch",
		Description: patchToolDescription,
	}, func(ctx context.Context, req *mcp.CallToolRequest, in patchInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
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

func handlePatchFunction(ctx context.Context, commands service.SurgeonCommands, in patchInput) (*mcp.CallToolResult, any, error) {
	var ops []patchOpInput
	raw, _ := json.Marshal(in.Patches)
	if err := json.Unmarshal(raw, &ops); err != nil {
		return errorResult(fmt.Sprintf("patches must be an array of function patch ops: %v", err)), nil, nil
	}
	patches := make([]domain.FunctionPatch, len(ops))
	for i, p := range ops {
		patches[i] = domain.FunctionPatch{
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
	result, err := commands.PatchFunction(ctx, domain.PatchFunctionRequest{
		FilePath:      in.File,
		Identifier:    in.Identifier,
		Patches:       patches,
		Preview:       in.Preview,
		IncludeNested: in.IncludeNested,
	})
	if err != nil {
		return errorResultWithCode(fmt.Sprintf("ERROR (patch function): %v", err), err), nil, nil
	}
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
		prefix = fmt.Sprintf("\u26a0 AUTO-LIFTED: %d patch(es) moved to the enclosing top-level statement\n\n", len(result.AutoLifts)) + prefix
	}
	if result.Diff != "" {
		res := textResult(prefix + "\n\n" + result.Diff)
		res.StructuredContent = patchOutput{File: in.File, Identifier: in.Identifier, Applied: result.Applied, Preview: result.Preview, Diff: result.Diff, AddedImports: result.AddedImports, Warnings: result.Warnings, AutoLifts: liftJSON}
		return res, nil, nil
	}
	res := textResult(prefix)
	res.StructuredContent = patchOutput{File: in.File, Identifier: in.Identifier, Applied: result.Applied, Preview: result.Preview, AddedImports: result.AddedImports, Warnings: result.Warnings, AutoLifts: liftJSON}
	return res, nil, nil
}

func handlePatchStruct(ctx context.Context, commands service.SurgeonCommands, in patchInput) (*mcp.CallToolResult, any, error) {
	var ops []structPatchOpInput
	raw, _ := json.Marshal(in.Patches)
	if err := json.Unmarshal(raw, &ops); err != nil {
		return errorResult(fmt.Sprintf("patches must be an array of struct patch ops: %v", err)), nil, nil
	}
	patches := make([]domain.StructPatch, len(ops))
	for i, p := range ops {
		patches[i] = domain.StructPatch{
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
	result, err := commands.PatchStruct(ctx, domain.PatchStructRequest{
		FilePath:   in.File,
		Identifier: in.Identifier,
		Patches:    patches,
		Preview:    in.Preview,
	})
	if err != nil {
		return errorResultWithCode(fmt.Sprintf("ERROR (patch struct): %v", err), err), nil, nil
	}
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
	if result.Diff != "" {
		res := textResult(prefix + "\n\n" + result.Diff)
		res.StructuredContent = patchOutput{File: in.File, Identifier: in.Identifier, Applied: result.Applied, Preview: result.Preview, Diff: result.Diff, AddedImports: result.AddedImports, Warnings: result.Warnings}
		return res, nil, nil
	}
	res := textResult(prefix)
	res.StructuredContent = patchOutput{File: in.File, Identifier: in.Identifier, Applied: result.Applied, Preview: result.Preview, AddedImports: result.AddedImports, Warnings: result.Warnings}
	return res, nil, nil
}

func handlePatchInterface(ctx context.Context, commands service.SurgeonCommands, in patchInput) (*mcp.CallToolResult, any, error) {
	var ops []interfacePatchOpInput
	raw, _ := json.Marshal(in.Patches)
	if err := json.Unmarshal(raw, &ops); err != nil {
		return errorResult(fmt.Sprintf("patches must be an array of interface patch ops: %v", err)), nil, nil
	}
	patches := make([]domain.InterfacePatch, len(ops))
	for i, p := range ops {
		patches[i] = domain.InterfacePatch{
			Op:        domain.InterfacePatchOp(p.Op),
			Name:      p.Name,
			From:      p.From,
			To:        p.To,
			Signature: p.Signature,
			Type:      p.Type,
			Doc:       p.Doc,
			Before:    p.Before,
			After:     p.After,
			Position:  p.Position,
		}
	}
	result, err := commands.PatchInterface(ctx, domain.PatchInterfaceRequest{
		FilePath:   in.File,
		Identifier: in.Identifier,
		Patches:    patches,
		Preview:    in.Preview,
		MockFile:   in.MockFile,
		MockName:   in.MockName,
	})
	if err != nil {
		return errorResultWithCode(fmt.Sprintf("ERROR (patch interface): %v", err), err), nil, nil
	}
	prefix := fmt.Sprintf("OK: %d patch(es) applied", result.Applied)
	if result.Preview {
		prefix = fmt.Sprintf("PREVIEW: %d patch(es) (not written)", result.Applied)
	}
	if result.MockUpdated {
		prefix += " (mock regenerated)"
	}
	if len(result.AddedImports) > 0 {
		prefix += fmt.Sprintf(" (imports added: %s)", strings.Join(result.AddedImports, ", "))
	}
	for _, w := range result.Warnings {
		prefix += "\n  WARNING: " + w
	}
	if result.Diff != "" {
		res := textResult(prefix + "\n\n" + result.Diff)
		res.StructuredContent = patchOutput{File: in.File, Identifier: in.Identifier, Applied: result.Applied, Preview: result.Preview, Diff: result.Diff, MockUpdated: result.MockUpdated, AddedImports: result.AddedImports, Warnings: result.Warnings}
		return res, nil, nil
	}
	res := textResult(prefix)
	res.StructuredContent = patchOutput{File: in.File, Identifier: in.Identifier, Applied: result.Applied, Preview: result.Preview, MockUpdated: result.MockUpdated, AddedImports: result.AddedImports, Warnings: result.Warnings}
	return res, nil, nil
}

func handlePatchFile(ctx context.Context, commands service.SurgeonCommands, in patchInput) (*mcp.CallToolResult, any, error) {
	var ops []filePatchOpInput
	raw, _ := json.Marshal(in.Patches)
	if err := json.Unmarshal(raw, &ops); err != nil {
		return errorResult(fmt.Sprintf("patches must be an array of file patch ops: %v", err)), nil, nil
	}
	patches := make([]domain.FilePatch, len(ops))
	for i, p := range ops {
		patches[i] = domain.FilePatch{
			Match:      p.Match,
			MatchRegex: p.MatchRegex,
			Replace:    p.Replace,
		}
	}
	result, err := commands.PatchFile(ctx, domain.PatchFileRequest{
		FilePath: in.File,
		Patches:  patches,
		Preview:  in.Preview,
		Scope:    in.Scope,
	})
	if err != nil {
		return errorResultWithCode(fmt.Sprintf("ERROR (patch file): %v", err), err), nil, nil
	}
	prefix := fmt.Sprintf("OK: %d patch(es) applied", result.Applied)
	if result.Preview {
		prefix = fmt.Sprintf("PREVIEW: %d patch(es) (not written)", result.Applied)
	}
	if len(result.Hits) > 0 {
		hitStrs := make([]string, len(result.Hits))
		for i, h := range result.Hits {
			hitStrs[i] = fmt.Sprintf("#%d=%d", i+1, h)
		}
		prefix += fmt.Sprintf(" [hits %s]", strings.Join(hitStrs, ", "))
	}
	if len(result.AddedImports) > 0 {
		prefix += fmt.Sprintf(" (imports added: %s)", strings.Join(result.AddedImports, ", "))
	}
	for _, w := range result.Warnings {
		prefix += "\n  WARNING: " + w
	}
	if result.Diff != "" {
		res := textResult(prefix + "\n\n" + result.Diff)
		res.StructuredContent = patchFileOutput{File: in.File, Applied: result.Applied, Hits: result.Hits, Preview: result.Preview, Diff: result.Diff, AddedImports: result.AddedImports, Warnings: result.Warnings}
		return res, nil, nil
	}
	res := textResult(prefix)
	res.StructuredContent = patchFileOutput{File: in.File, Applied: result.Applied, Hits: result.Hits, Preview: result.Preview, AddedImports: result.AddedImports, Warnings: result.Warnings}
	return res, nil, nil
}

func handlePatchDecl(ctx context.Context, commands service.SurgeonCommands, in patchInput) (*mcp.CallToolResult, any, error) {
	var ops []patchOpInput
	raw, _ := json.Marshal(in.Patches)
	if err := json.Unmarshal(raw, &ops); err != nil {
		return errorResult(fmt.Sprintf("patches must be an array of decl patch ops: %v", err)), nil, nil
	}
	patches := make([]domain.FunctionPatch, len(ops))
	for i, p := range ops {
		patches[i] = domain.FunctionPatch{
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
		FilePath:   in.File,
		Identifier: in.Identifier,
		Patches:    patches,
		Preview:    in.Preview,
	})
	if err != nil {
		return errorResultWithCode(fmt.Sprintf("ERROR (patch decl): %v", err), err), nil, nil
	}
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
	if result.Diff != "" {
		res := textResult(prefix + "\n\n" + result.Diff)
		res.StructuredContent = patchOutput{File: in.File, Identifier: in.Identifier, Applied: result.Applied, Preview: result.Preview, Diff: result.Diff, AddedImports: result.AddedImports, Warnings: result.Warnings}
		return res, nil, nil
	}
	res := textResult(prefix)
	res.StructuredContent = patchOutput{File: in.File, Identifier: in.Identifier, Applied: result.Applied, Preview: result.Preview, AddedImports: result.AddedImports, Warnings: result.Warnings}
	return res, nil, nil
}
