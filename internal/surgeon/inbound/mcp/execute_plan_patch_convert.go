package mcp

import (
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

// joinParams renders an array of parameter declarations into the
// parenthesised list form expected by domain.FunctionPatch.Params
// ("(a string, b int)"). Returns an empty string when the input is empty,
// which signals "keep current params" to the patch engine.
func joinParams(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// toFunctionPatches converts MCP patchOpInput items (shared with patch_function
// and patch_decl tool inputs) into the domain.FunctionPatch shape used by
// PatchFunction / PatchDecl requests.
func toFunctionPatches(in []patchOpInput) []domain.FunctionPatch {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.FunctionPatch, len(in))
	for i, p := range in {
		out[i] = domain.FunctionPatch{
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
	return out
}

// toStructPatches converts MCP structPatchOpInput items into domain.StructPatch.
func toStructPatches(in []structPatchOpInput) []domain.StructPatch {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.StructPatch, len(in))
	for i, p := range in {
		out[i] = domain.StructPatch{
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
	return out
}

// toInterfacePatches converts MCP interfacePatchOpInput items into
// domain.InterfacePatch.
func toInterfacePatches(in []interfacePatchOpInput) []domain.InterfacePatch {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.InterfacePatch, len(in))
	for i, p := range in {
		out[i] = domain.InterfacePatch{
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
	return out
}

// toFilePatches converts MCP filePatchOpInput items into domain.FilePatch.
func toFilePatches(in []filePatchOpInput) []domain.FilePatch {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.FilePatch, len(in))
	for i, p := range in {
		out[i] = domain.FilePatch{
			Match:        p.Match,
			MatchRegex:   p.MatchRegex,
			Replace:      p.Replace,
			MatchLiteral: p.MatchLiteral,
			Occurrence:   p.Occurrence,
			MatchMode:    p.MatchMode,
		}
	}
	return out
}
