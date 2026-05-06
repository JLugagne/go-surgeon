package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- Action tools (create / update / delete) ---

type createInput struct {
	Object     string `json:"object" jsonschema:"what to create: file, func, or struct"`
	File       string `json:"file" jsonschema:"target file path"`
	Content    string `json:"content" jsonschema:"raw Go source code, no package declaration or imports"`
	Identifier string `json:"identifier,omitempty" jsonschema:"ignored — identifier is inferred from content; accepted to avoid validation errors"`
	WithTest   bool   `json:"with_test,omitempty" jsonschema:"generate a test skeleton alongside the function (only applies when object=func)"`
	Preview    bool   `json:"preview,omitempty" jsonschema:"if true, return diff without writing the file"`
}

type updateInput struct {
	Object     string `json:"object" jsonschema:"what to update: file, func, or struct"`
	File       string `json:"file" jsonschema:"target file path"`
	Identifier string `json:"identifier,omitempty" jsonschema:"AST identifier, e.g. FuncName or Receiver.Method, required for func and struct"`
	Content    string `json:"content" jsonschema:"raw Go source code, no package declaration or imports"`
	Doc        string `json:"doc,omitempty" jsonschema:"set or replace the doc comment (raw text without // prefix)"`
	StripDoc   bool   `json:"strip_doc,omitempty" jsonschema:"remove the existing doc comment"`
	WithTest   bool   `json:"with_test,omitempty" jsonschema:"generate a test skeleton alongside the function (only applies when object=func)"`
	Preview    bool   `json:"preview,omitempty" jsonschema:"if true, return diff without writing the file"`
}

type deleteInput struct {
	Object     string `json:"object" jsonschema:"what to delete: func or struct"`
	File       string `json:"file" jsonschema:"target file path"`
	Identifier string `json:"identifier,omitempty" jsonschema:"AST identifier (e.g. FuncName or Receiver.Method); not required when object=file"`
	Preview    bool   `json:"preview,omitempty" jsonschema:"if true, return diff without writing the file"`
}

var createObjectMap = map[string]domain.ActionType{
	"file":   domain.ActionTypeCreateFile,
	"func":   domain.ActionTypeAddFunc,
	"struct": domain.ActionTypeAddStruct,
}

var updateObjectMap = map[string]domain.ActionType{
	"file":   domain.ActionTypeReplaceFile,
	"func":   domain.ActionTypeUpdateFunc,
	"struct": domain.ActionTypeUpdateStruct,
}

var deleteObjectMap = map[string]domain.ActionType{
	"func":   domain.ActionTypeDeleteFunc,
	"struct": domain.ActionTypeDeleteStruct,
	"file":   domain.ActionTypeDeleteFile,
}

func registerActionTools(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "create",
		Description: "Add a new file, function, or struct. object='file' creates a new file; 'func'/'struct' append to an existing file; 'auto' infers from content (func ... → func, type ... struct → struct, else file). When adding several related items, use execute_plan instead.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in createInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		var actionType domain.ActionType
		if in.Object == "auto" {
			trimmed := strings.TrimSpace(in.Content)
			if strings.HasPrefix(trimmed, "func ") {
				actionType = domain.ActionTypeAddFunc
			} else if strings.Contains(trimmed, "type ") && strings.Contains(trimmed, "struct {") {
				actionType = domain.ActionTypeAddStruct
			} else {
				actionType = domain.ActionTypeCreateFile
			}
		} else {
			var ok bool
			actionType, ok = createObjectMap[in.Object]
			if !ok {
				return errorResult(fmt.Sprintf("invalid object %q: must be file, func, struct, or auto", in.Object)), nil, nil
			}
		}

		result, err := commands.ExecutePlan(ctx, domain.Plan{
			Preview: in.Preview,
			Actions: []domain.Action{{
				Action:   actionType,
				FilePath: in.File,
				Content:  in.Content,
				WithTest: in.WithTest,
			}},
		})
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (create %s): %v", in.Object, err), err), nil, nil
		}

		var sb strings.Builder
		for _, w := range result.Warnings {
			fmt.Fprintf(&sb, "WARNING: %s\n", w)
		}
		verb := "SUCCESS"
		if result.Preview {
			verb = "PREVIEW"
		}
		fmt.Fprintf(&sb, "%s (create %s): %d files modified", verb, in.Object, result.FilesModified)
		if result.Diff != "" {
			sb.WriteString("\n\n")
			sb.WriteString(result.Diff)
		}
		res := textResult(sb.String())
		res.StructuredContent = editOutput{
			FilesModified: result.Files,
			Symbols:       []symbolEdit{{Action: string(actionType), File: in.File}},
			Warnings:      result.Warnings,
			AddedImports:  result.AddedImports,
			Preview:       result.Preview,
			Diff:          result.Diff,
		}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update",
		Description: "Replace a whole function, method, struct, or file. For small changes inside a function body, prefer patch_function. content is the complete new declaration (signature + body). Doc comments are kept unless you set doc or strip_doc=true. preview=true returns a unified diff without writing. object='auto' infers from content (func ... → func, type ... struct → struct, else file).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in updateInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		var actionType domain.ActionType
		if in.Object == "auto" {
			trimmed := strings.TrimSpace(in.Content)
			if strings.HasPrefix(trimmed, "func ") {
				actionType = domain.ActionTypeUpdateFunc
			} else if strings.Contains(trimmed, "type ") && strings.Contains(trimmed, "struct {") {
				actionType = domain.ActionTypeUpdateStruct
			} else {
				actionType = domain.ActionTypeReplaceFile
			}
		} else {
			var ok bool
			actionType, ok = updateObjectMap[in.Object]
			if !ok {
				return errorResult(fmt.Sprintf("invalid object %q: must be file, func, struct, or auto", in.Object)), nil, nil
			}
		}

		result, err := commands.ExecutePlan(ctx, domain.Plan{
			Preview: in.Preview,
			Actions: []domain.Action{{
				Action:     actionType,
				FilePath:   in.File,
				Identifier: in.Identifier,
				Content:    in.Content,
				Doc:        in.Doc,
				StripDoc:   in.StripDoc,
				WithTest:   in.WithTest,
			}},
		})
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (update %s): %v", in.Object, err), err), nil, nil
		}

		var sb strings.Builder
		for _, w := range result.Warnings {
			fmt.Fprintf(&sb, "WARNING: %s\n", w)
		}
		verb := "SUCCESS"
		if result.Preview {
			verb = "PREVIEW"
		}
		fmt.Fprintf(&sb, "%s (update %s): %d files modified", verb, in.Object, result.FilesModified)
		if result.Diff != "" {
			sb.WriteString("\n\n")
			sb.WriteString(result.Diff)
		}
		res := textResult(sb.String())
		res.StructuredContent = editOutput{
			FilesModified: result.Files,
			Symbols:       []symbolEdit{{Action: string(actionType), Identifier: in.Identifier, File: in.File}},
			Warnings:      result.Warnings,
			AddedImports:  result.AddedImports,
			Preview:       result.Preview,
			Diff:          result.Diff,
		}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete",
		Description: "Remove a function, method, struct, or file. object='file' deletes the file from disk (no identifier needed); object='struct' also removes every method on the struct across the package. For interfaces, use the interface tool with action=delete instead (handles mock cleanup). preview=true returns a unified diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in deleteInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		actionType, ok := deleteObjectMap[in.Object]
		if !ok {
			return errorResult(fmt.Sprintf("invalid object %q: must be file, func, or struct", in.Object)), nil, nil
		}

		result, err := commands.ExecutePlan(ctx, domain.Plan{
			Preview: in.Preview,
			Actions: []domain.Action{{
				Action:     actionType,
				FilePath:   in.File,
				Identifier: in.Identifier,
			}},
		})
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (delete %s): %v", in.Object, err), err), nil, nil
		}

		var sb strings.Builder
		for _, w := range result.Warnings {
			fmt.Fprintf(&sb, "WARNING: %s\n", w)
		}
		verb := "SUCCESS"
		if result.Preview {
			verb = "PREVIEW"
		}
		fmt.Fprintf(&sb, "%s (delete %s): %d files modified", verb, in.Object, result.FilesModified)
		if result.Diff != "" {
			sb.WriteString("\n\n")
			sb.WriteString(result.Diff)
		}
		res := textResult(sb.String())
		res.StructuredContent = editOutput{
			FilesModified: result.Files,
			Symbols:       []symbolEdit{{Action: string(actionType), Identifier: in.Identifier, File: in.File}},
			Warnings:      result.Warnings,
			AddedImports:  result.AddedImports,
			Preview:       result.Preview,
			Diff:          result.Diff,
		}
		return res, nil, nil
	})
}
