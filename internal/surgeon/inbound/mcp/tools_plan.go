package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type executePlanInput struct {
	Actions []executePlanActionInput `json:"actions" jsonschema:"ordered list of AST actions to execute atomically (up to 15)"`
	Preview bool                     `json:"preview,omitempty" jsonschema:"if true, return diff without writing any files"`
}

type executePlanActionInput struct {
	Action            string                  `json:"action" jsonschema:"action type: create_file, replace_file, add_func, update_func, delete_func, add_struct, update_struct, delete_struct, add_interface, update_interface, delete_interface, insert_call, patch_function, patch_struct, patch_interface, patch_file, patch_decl"`
	File              string                  `json:"file" jsonschema:"target file path"`
	Package           string                  `json:"package,omitempty" jsonschema:"package import path (for cross-package operations)"`
	Identifier        string                  `json:"identifier,omitempty" jsonschema:"AST identifier, e.g. FuncName or Receiver.Method"`
	Content           string                  `json:"content,omitempty" jsonschema:"raw Go source code, no package declaration or imports"`
	MockFile          string                  `json:"mock_file,omitempty" jsonschema:"target file for the generated mock"`
	MockName          string                  `json:"mock_name,omitempty" jsonschema:"name of the mock struct"`
	Doc               string                  `json:"doc,omitempty" jsonschema:"set or replace the doc comment (raw text without // prefix)"`
	StripDoc          bool                    `json:"strip_doc,omitempty" jsonschema:"remove the existing doc comment"`
	Position          string                  `json:"position,omitempty" jsonschema:"insert position: before-return, end-of-body, or after:<marker>"`
	WithTest          bool                    `json:"with_test,omitempty" jsonschema:"generate a test skeleton alongside the function"`
	PatchFunctionOps  []patchOpInput          `json:"patch_function_ops,omitempty" jsonschema:"patch operations for patch_function actions (same shape as patch_function tool's patches)"`
	PatchStructOps    []structPatchOpInput    `json:"patch_struct_ops,omitempty" jsonschema:"patch operations for patch_struct actions"`
	PatchInterfaceOps []interfacePatchOpInput `json:"patch_interface_ops,omitempty" jsonschema:"patch operations for patch_interface actions"`
	PatchFileOps      []filePatchOpInput      `json:"patch_file_ops,omitempty" jsonschema:"patch operations for patch_file actions"`
	PatchDeclOps      []patchOpInput          `json:"patch_decl_ops,omitempty" jsonschema:"patch operations for patch_decl actions (same shape as patch_function ops)"`
}

func registerExecutePlanTool(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "execute_plan",
		Description: "Apply up to 15 related AST edits atomically — any failure rolls everything back. " +
			"Actions: create_file, replace_file, add_func, update_func, delete_func, add_struct, update_struct, delete_struct, add_interface, update_interface, delete_interface, insert_call, patch_function, patch_struct, patch_interface, patch_file, patch_decl. " +
			"In-place patch actions (patch_function, patch_struct, patch_interface, patch_file, patch_decl) carry their operations in dedicated fields: patch_function_ops, patch_struct_ops, patch_interface_ops, patch_file_ops, patch_decl_ops.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in executePlanInput) (*mcp.CallToolResult, any, error) {
		actions := make([]domain.Action, len(in.Actions))
		for i, a := range in.Actions {
			actions[i] = domain.Action{
				Action:            domain.ActionType(a.Action),
				FilePath:          a.File,
				PackagePath:       a.Package,
				Identifier:        a.Identifier,
				Content:           a.Content,
				MockFile:          a.MockFile,
				MockName:          a.MockName,
				Doc:               a.Doc,
				StripDoc:          a.StripDoc,
				Position:          domain.InsertPosition(a.Position),
				WithTest:          a.WithTest,
				PatchFunctionOps:  toFunctionPatches(a.PatchFunctionOps),
				PatchStructOps:    toStructPatches(a.PatchStructOps),
				PatchInterfaceOps: toInterfacePatches(a.PatchInterfaceOps),
				PatchFileOps:      toFilePatches(a.PatchFileOps),
				PatchDeclOps:      toFunctionPatches(a.PatchDeclOps),
			}
			if actions[i].FilePath != "" {
				if err := validateGoFile(actions[i].FilePath); err != nil {
					return err, nil, nil
				}
			}
		}
		plan := domain.Plan{Preview: in.Preview, Actions: actions}

		result, err := commands.ExecutePlan(ctx, plan)
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("plan execution failed: %v", err), err), nil, nil
		}

		var sb strings.Builder
		for _, w := range result.Warnings {
			fmt.Fprintf(&sb, "WARNING: %s\n", w)
		}
		verb := "SUCCESS"
		if result.Preview {
			verb = "PREVIEW"
		}
		fmt.Fprintf(&sb, "%s: %d files modified", verb, result.FilesModified)
		if result.Diff != "" {
			sb.WriteString("\n\n")
			sb.WriteString(result.Diff)
		}
		symbols := make([]symbolEdit, len(in.Actions))
		for i, a := range in.Actions {
			symbols[i] = symbolEdit{Action: a.Action, Identifier: a.Identifier, File: a.File}
		}
		res := textResult(sb.String())
		res.StructuredContent = editOutput{
			FilesModified: result.Files,
			Symbols:       symbols,
			Warnings:      result.Warnings,
			AddedImports:  result.AddedImports,
			Preview:       result.Preview,
			Diff:          result.Diff,
		}
		return res, nil, nil
	})
}
