package mcp

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
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
	Object     string `json:"object,omitempty" jsonschema:"what to update: file, func, struct, or auto (default); auto infers from content"`
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
			actionType = classifyCreateContent(in.Content)
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
		Description: "Replace a whole function, method, struct, or file. content is the complete new declaration. For small changes inside a function body, prefer patch target=function. object defaults to 'auto', which infers from content (func/type/file). Doc comments are kept unless doc or strip_doc=true. preview=true returns a diff.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in updateInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		if in.Object == "" {
			in.Object = "auto"
		}
		var actionType domain.ActionType
		if in.Object == "auto" {
			var errResult *mcp.CallToolResult
			if actionType, errResult = classifyUpdateContent(in.Content); errResult != nil {
				return errResult, nil, nil
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

// classifyDeclContent parses decl-only content and reports what its single
// top-level declaration is: "func", "struct", "interface", "decl" (const /
// var / other type declarations), "file" (content carries its own package
// clause), or "multi" (several declarations). ok=false means the content is
// not parseable Go. Parsing replaces the previous string-prefix sniffing,
// which misrouted doc-comment-prefixed declarations, single-line structs
// (`type T struct{ … }`), interfaces and type aliases.
func classifyDeclContent(content string) (string, bool) {
	fset := token.NewFileSet()
	if f, err := parser.ParseFile(fset, "sniff.go", content, parser.PackageClauseOnly); err == nil && f.Name != nil {
		return "file", true
	}
	f, err := parser.ParseFile(fset, "sniff.go", "package sniff\n"+content, parser.SkipObjectResolution)
	if err != nil || len(f.Decls) == 0 {
		return "", false
	}
	if len(f.Decls) > 1 {
		return "multi", true
	}
	switch d := f.Decls[0].(type) {
	case *ast.FuncDecl:
		return "func", true
	case *ast.GenDecl:
		if d.Tok == token.TYPE && len(d.Specs) == 1 {
			if ts, ok := d.Specs[0].(*ast.TypeSpec); ok {
				switch ts.Type.(type) {
				case *ast.StructType:
					return "struct", true
				case *ast.InterfaceType:
					return "interface", true
				}
			}
		}
		return "decl", true
	default:
		return "", false
	}
}

// classifyCreateContent maps object=auto create content to an action type.
// Funcs and structs (including doc-comment-prefixed and single-line forms)
// route to append actions; everything else keeps the historical create_file
// fallback, which is safe for create: an existing target file fails with
// FILE_ALREADY_EXISTS instead of being overwritten.
func classifyCreateContent(content string) domain.ActionType {
	kind, ok := classifyDeclContent(content)
	if !ok {
		return domain.ActionTypeCreateFile
	}
	switch kind {
	case "func":
		return domain.ActionTypeAddFunc
	case "struct":
		return domain.ActionTypeAddStruct
	default:
		return domain.ActionTypeCreateFile
	}
}

// classifyUpdateContent maps object=auto update content to an action type.
// It refuses to guess when content is not a single declaration: the old
// fallback silently routed anything unrecognized to replace_file, which
// rewrites the whole target file and destroys every other declaration in
// it. Content carrying its own package clause is the only shape still
// treated as a whole-file replacement.
func classifyUpdateContent(content string) (domain.ActionType, *mcp.CallToolResult) {
	kind, ok := classifyDeclContent(content)
	if !ok {
		return "", errorResult("update object=auto: content is not parseable Go (neither a single declaration nor a full file with a package clause) — fix the content or pass object explicitly (file, func, struct)")
	}
	switch kind {
	case "func":
		return domain.ActionTypeUpdateFunc, nil
	case "struct":
		return domain.ActionTypeUpdateStruct, nil
	case "interface":
		return domain.ActionTypeUpdateInterface, nil
	case "decl":
		return domain.ActionTypeUpdateDecl, nil
	case "file":
		return domain.ActionTypeReplaceFile, nil
	default:
		return "", errorResult("update object=auto: content contains multiple declarations; update targets a single declaration — use execute_plan for several edits, or pass object=file with full file content to replace the whole file")
	}
}
