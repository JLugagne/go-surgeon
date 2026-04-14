package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/JLugagne/go-surgeon/internal/surgeon/inbound/converters"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const serverInstructions = `You have access to go-surgeon, an AST-based code editor for Go.
For ANY operation on Go files (.go), you MUST use go-surgeon tools — never Edit, Write, Read, Grep, Glob, or Bash on .go files.
This rule applies throughout the entire task, including when editing, not just at the start.

Exploration (use instead of Read/Grep/Glob/Bash on .go files):
- graph: explore package structure. Start here on any Go project.
- symbol: read a function, method, or struct body. Use body=true before any edit.
- To read a third-party dependency's source (signatures, internals, usage examples),
  set module='github.com/org/repo' on graph or symbol. Do NOT fall back to find/grep/cat
  inside $GOMODCACHE — the module parameter exists exactly for this purpose.

Editing (use instead of Edit/Write/Bash on .go files):
- create: add a new file, function, or struct
- update: replace a function, method, struct, or file
- delete: remove a function, method, or struct
- patch_function: surgical text-match edits inside one function body (replace, insert_before/after, delete, wrap) — use instead of update for small in-body changes
- patch_struct: granular field edits on a struct (add_field, remove_field, rename_field, retype_field, set_tag, set_doc) — use instead of update for single-field changes
- patch_interface: granular method edits on an interface (add_method, remove_method, rename_method, retype_method, set_doc, embed, remove_embed) — use instead of update_interface for single-method changes; regenerates mocks automatically
- insert_call: insert a single statement inside a function body (before-return, end-of-body, after:<marker>)
- execute_plan: multiple edits in one shot (up to 15 actions, preferred for multi-step changes)

Interface & mock management:
- add_interface / update_interface / delete_interface: manage interfaces and their mocks
  - There is no add_interface_method tool. To add a method to an existing interface:
    1. symbol body=true to read the current interface
    2. update_interface with the complete new declaration (existing methods + new one)
- implement: generate stubs for an interface you don't own
- mock: generate a standalone mock for any interface
- extract_interface: extract an interface from an existing struct

Code generation:
- test: generate a table-driven test skeleton
- tag: add or update struct field tags (json, bson, etc.)

When to use each editing tool:
- Add a new symbol (func/struct/file) → create
- Replace a whole function, struct, or file → update
- Delete a whole symbol → delete
- Change a few lines *inside* a function body → patch_function (avoids re-emitting the whole body)
- Add/remove/rename/retype a single field or tag on a struct → patch_struct (avoids re-emitting the whole struct)
- Add/remove/rename/retype a single method or embed on an interface → patch_interface (replaces the old 'read+update_interface' workflow; regenerates the mock too)
- Insert one statement at a fixed position → insert_call
- Multiple coordinated edits → execute_plan

Rules that apply to all tools:
- Never include package declarations or import blocks in content — goimports runs automatically.
- Always call symbol with body=true before updating or deleting.
- identifier format:
  - FuncName                — free function
  - Receiver.Method         — method on a type (pointer or value receiver)
  - StructName              — struct type
  - InterfaceName           — interface type (same shape as struct: just the type name)
  - pkg.Name                — package-qualified form, accepted where ambiguity matters`

// NewServer creates an MCP server with all go-surgeon tools registered.
func NewServer(commands service.SurgeonCommands, queries service.SurgeonQueries) *mcp.Server {
	s := mcp.NewServer(
		&mcp.Implementation{
			Name:    "go-surgeon",
			Version: "1.0.0",
		},
		&mcp.ServerOptions{
			Instructions: serverInstructions,
		},
	)

	registerQueryTools(s, queries)
	registerActionTools(s, commands)
	registerInterfaceTools(s, commands)
	registerInsertCallTool(s, commands)
	registerOtherTools(s, commands)
	registerPatchTools(s, commands)

	return s
}

// --- Query tools ---

type graphInput struct {
	Dir         string   `json:"dir,omitempty" jsonschema:"directory to walk, defaults to current directory"`
	Symbols     bool     `json:"symbols,omitempty" jsonschema:"include exported symbols per file"`
	Summary     bool     `json:"summary,omitempty" jsonschema:"append package doc comment summary"`
	Deps        bool     `json:"deps,omitempty" jsonschema:"show internal package import dependencies"`
	Recursive   bool     `json:"recursive,omitempty" jsonschema:"walk sub-packages when symbols is set"`
	Tests       bool     `json:"tests,omitempty" jsonschema:"include test files"`
	Depth       int      `json:"depth,omitempty" jsonschema:"limit directory recursion depth, 0 means unlimited"`
	Focus       string   `json:"focus,omitempty" jsonschema:"package path for full detail, others show path only"`
	Exclude     []string `json:"exclude,omitempty" jsonschema:"glob patterns for directories to skip"`
	TokenBudget int      `json:"token_budget,omitempty" jsonschema:"approximate max tokens in output, 0 means unlimited"`
	Module      string   `json:"module,omitempty" jsonschema:"import path of a dependency to explore instead of the current project, e.g. 'github.com/spf13/cobra'; dir and focus are relative to the module root when set"`
}

type symbolInput struct {
	Query  string `json:"query" jsonschema:"symbol name to search for, supports Name or Receiver.Method or pkg.Name forms"`
	Body   bool   `json:"body,omitempty" jsonschema:"show the full function or struct body with line numbers"`
	Tests  bool   `json:"tests,omitempty" jsonschema:"include test files in the search"`
	Dir    string `json:"dir,omitempty" jsonschema:"directory to search in, defaults to current directory"`
	Module string `json:"module,omitempty" jsonschema:"import path of a dependency to search in instead of the current project, e.g. 'github.com/spf13/cobra'"`
}

func registerQueryTools(s *mcp.Server, queries service.SurgeonQueries) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "graph",
		Description: "Explore a Go project's package structure — use this instead of find/ls/glob for any Go codebase. Start with no arguments to see all packages; use focus='pkg/path' for full detail on one package (symbols + summary + recursive); use symbols=true for a broad symbol overview. Set module='github.com/org/repo' to explore a third-party dependency's source instead of the current project — use this instead of find/grep/cat inside $GOMODCACHE. Use token_budget to cap output on large projects.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in graphInput) (*mcp.CallToolResult, any, error) {
		dir := in.Dir
		if dir == "" {
			dir = "."
		}

		opts := domain.GraphOptions{
			Dir:         dir,
			Symbols:     in.Symbols,
			Summary:     in.Summary,
			Deps:        in.Deps,
			Recursive:   in.Recursive,
			Tests:       in.Tests,
			Depth:       in.Depth,
			Focus:       in.Focus,
			Exclude:     in.Exclude,
			TokenBudget: in.TokenBudget,
			Module:      in.Module,
		}

		if opts.Focus != "" {
			opts.Symbols = true
			opts.Summary = true
			opts.Recursive = true
		}

		packages, err := queries.Graph(ctx, opts)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to build graph: %v", err)), nil, nil
		}

		text := formatGraph(packages, opts)
		return textResult(text), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "symbol",
		Description: "Look up a function, method, struct, or interface by name — use this instead of reading whole files. Always call with body=true before editing to see the current implementation. Query formats: 'Name' (any func/struct), 'Receiver.Method' (method on a type), 'pkg.Name' (package-qualified). Set module='github.com/org/repo' to search inside a third-party dependency instead of the current project. Returns signature, file location with line numbers, and optionally the full body. If multiple matches are returned, refine with 'Receiver.Method' or scope with dir.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in symbolInput) (*mcp.CallToolResult, any, error) {
		dir := in.Dir
		if dir == "" {
			dir = "."
		}

		results := findSymbols(ctx, queries, in.Query, in.Tests, dir, in.Module)
		if len(results) == 0 {
			return textResult(fmt.Sprintf("No matches found for '%s'.\nHint: run 'graph' with symbols=true and dir set to list available symbols.", in.Query)), nil, nil
		}

		text := formatSymbolResults(results, in.Body, in.Query)
		return textResult(text), nil, nil
	})
}

// --- Action tools (create / update / delete) ---

type createInput struct {
	Object     string `json:"object" jsonschema:"what to create: file, func, or struct"`
	File       string `json:"file" jsonschema:"target file path"`
	Content    string `json:"content" jsonschema:"raw Go source code, no package declaration or imports"`
	Identifier string `json:"identifier,omitempty" jsonschema:"ignored — identifier is inferred from content; accepted to avoid validation errors"`
	WithTest   bool   `json:"with_test,omitempty" jsonschema:"generate a test skeleton alongside the function (only applies when object=func)"`
}

type updateInput struct {
	Object     string `json:"object" jsonschema:"what to update: file, func, or struct"`
	File       string `json:"file" jsonschema:"target file path"`
	Identifier string `json:"identifier,omitempty" jsonschema:"AST identifier, e.g. FuncName or Receiver.Method, required for func and struct"`
	Content    string `json:"content" jsonschema:"raw Go source code, no package declaration or imports"`
	Doc        string `json:"doc,omitempty" jsonschema:"set or replace the doc comment (raw text without // prefix)"`
	StripDoc   bool   `json:"strip_doc,omitempty" jsonschema:"remove the existing doc comment"`
	WithTest   bool   `json:"with_test,omitempty" jsonschema:"generate a test skeleton alongside the function (only applies when object=func)"`
}

type deleteInput struct {
	Object     string `json:"object" jsonschema:"what to delete: func or struct"`
	File       string `json:"file" jsonschema:"target file path"`
	Identifier string `json:"identifier" jsonschema:"AST identifier, e.g. FuncName or Receiver.Method"`
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
}

func registerActionTools(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "create",
		Description: "Add a new file (object='file'), free function (object='func'), or struct definition (object='struct') to a Go package — use this instead of Write or Edit to create Go code. Content is raw Go code — never include package declarations or import blocks, goimports runs automatically and manages all imports. For object='file' the path must not already exist. Prefer execute_plan when creating multiple items together.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in createInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		actionType, ok := createObjectMap[in.Object]
		if !ok {
			return errorResult(fmt.Sprintf("invalid object %q: must be file, func, or struct", in.Object)), nil, nil
		}

		result, err := commands.ExecutePlan(ctx, domain.Plan{Actions: []domain.Action{{
			Action:   actionType,
			FilePath: in.File,
			Content:  in.Content,
			WithTest: in.WithTest,
		}}})
		if err != nil {
			return errorResult(fmt.Sprintf("ERROR (create %s): %v", in.Object, err)), nil, nil
		}

		var sb strings.Builder
		for _, w := range result.Warnings {
			fmt.Fprintf(&sb, "WARNING: %s\n", w)
		}
		fmt.Fprintf(&sb, "SUCCESS (create %s): %d files modified", in.Object, result.FilesModified)
		return textResult(sb.String()), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update",
		Description: "Replace an existing function, method, struct, or entire file — use this instead of Edit or Write to modify Go code. For object='func' or 'struct', identifier is required: use 'FuncName' for free functions, 'Receiver.Method' for methods, 'StructName' for structs. Content must be the complete new declaration (full signature and body). Never include package declarations or imports — goimports handles all import changes. Always read the current code with symbol body=true first. Doc comments are preserved by default; set doc to replace them or strip_doc=true to remove them.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in updateInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		actionType, ok := updateObjectMap[in.Object]
		if !ok {
			return errorResult(fmt.Sprintf("invalid object %q: must be file, func, or struct", in.Object)), nil, nil
		}

		result, err := commands.ExecutePlan(ctx, domain.Plan{Actions: []domain.Action{{
			Action:     actionType,
			FilePath:   in.File,
			Identifier: in.Identifier,
			Content:    in.Content,
			Doc:        in.Doc,
			StripDoc:   in.StripDoc,
			WithTest:   in.WithTest,
		}}})
		if err != nil {
			return errorResult(fmt.Sprintf("ERROR (update %s): %v", in.Object, err)), nil, nil
		}

		var sb strings.Builder
		for _, w := range result.Warnings {
			fmt.Fprintf(&sb, "WARNING: %s\n", w)
		}
		fmt.Fprintf(&sb, "SUCCESS (update %s): %d files modified", in.Object, result.FilesModified)
		return textResult(sb.String()), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete",
		Description: "Remove a function, method, or struct from a Go file. object='func' handles both free functions (identifier='FuncName') and methods (identifier='Receiver.Method'). object='struct' deletes the struct AND all its methods across every file in the package — use with care. Does not delete associated mocks.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in deleteInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		actionType, ok := deleteObjectMap[in.Object]
		if !ok {
			return errorResult(fmt.Sprintf("invalid object %q: must be func or struct", in.Object)), nil, nil
		}

		result, err := commands.ExecutePlan(ctx, domain.Plan{Actions: []domain.Action{{
			Action:     actionType,
			FilePath:   in.File,
			Identifier: in.Identifier,
		}}})
		if err != nil {
			return errorResult(fmt.Sprintf("ERROR (delete %s): %v", in.Object, err)), nil, nil
		}

		var sb strings.Builder
		for _, w := range result.Warnings {
			fmt.Fprintf(&sb, "WARNING: %s\n", w)
		}
		fmt.Fprintf(&sb, "SUCCESS (delete %s): %d files modified", in.Object, result.FilesModified)
		return textResult(sb.String()), nil, nil
	})
}

// --- Interface tools ---

type interfaceInput struct {
	File       string `json:"file" jsonschema:"file containing the interface, required"`
	Identifier string `json:"identifier,omitempty" jsonschema:"interface name, required for update and delete"`
	Content    string `json:"content,omitempty" jsonschema:"raw Go interface source, no package declaration or imports"`
	MockFile   string `json:"mock_file,omitempty" jsonschema:"target file for the generated mock (add/update), or file containing the mock to delete (delete)"`
	MockName   string `json:"mock_name,omitempty" jsonschema:"name of the mock struct"`
	Doc        string `json:"doc,omitempty" jsonschema:"set or replace the doc comment (raw text without // prefix, update only)"`
	StripDoc   bool   `json:"strip_doc,omitempty" jsonschema:"remove the existing doc comment (update only)"`
	DeleteMock bool   `json:"delete_mock,omitempty" jsonschema:"delete only: also remove the mock struct, its methods and its compile-time assertion from mock_file; requires mock_file and mock_name"`
}

func registerInterfaceTools(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_interface",
		Description: "Add a new interface to a Go file and optionally generate a function-field mock in one step — use this instead of create when adding an interface. Always set mock_file and mock_name to generate the mock atomically; creating the mock separately with create or Write is error-prone. The generated mock uses func fields (e.g. CreateFunc) with a compile-time interface assertion.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in interfaceInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		result, err := commands.AddInterface(ctx, domain.InterfaceActionRequest{
			FilePath: in.File, Identifier: in.Identifier, Content: in.Content,
			MockFile: in.MockFile, MockName: in.MockName,
		})
		if err != nil {
			return errorResult(fmt.Sprintf("ERROR (add_interface): %v", err)), nil, nil
		}
		return textResult(result), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_interface",
		Description: "Update an existing interface and automatically regenerate its mock — use this instead of update when modifying an interface. There is no add_interface_method tool: to add a method to an existing interface, read the current body with symbol body=true, add the method to the content, then call update_interface with the complete new declaration. Always provide mock_file and mock_name so the mock stays in sync; updating the mock manually with update is error-prone and will drift. Content must be the complete new interface declaration without package declarations or imports. Doc comments are preserved by default; set doc to replace them or strip_doc=true to remove them.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in interfaceInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		result, err := commands.UpdateInterface(ctx, domain.InterfaceActionRequest{
			FilePath: in.File, Identifier: in.Identifier, Content: in.Content,
			MockFile: in.MockFile, MockName: in.MockName,
			Doc: in.Doc, StripDoc: in.StripDoc,
		})
		if err != nil {
			return errorResult(fmt.Sprintf("ERROR (update_interface): %v", err)), nil, nil
		}
		return textResult(result), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_interface",
		Description: "Delete an interface from a Go file. Set delete_mock=true together with mock_file and mock_name to also remove the mock struct, its methods, and the compile-time assertion (var _ I = (*MockI)(nil)) from mock_file — this is the safe default when you own the mock. The mock file itself is kept intact even if it becomes empty, so other mocks sharing the file are not disturbed. Without delete_mock, you must manually clean up the mock or builds will break.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in interfaceInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		if in.MockFile != "" {
			if err := validateGoFile(in.MockFile); err != nil {
				return err, nil, nil
			}
		}
		result, err := commands.DeleteInterface(ctx, domain.InterfaceActionRequest{
			FilePath:   in.File,
			Identifier: in.Identifier,
			MockFile:   in.MockFile,
			MockName:   in.MockName,
			DeleteMock: in.DeleteMock,
		})
		if err != nil {
			return errorResult(fmt.Sprintf("ERROR (delete_interface): %v", err)), nil, nil
		}
		return textResult(result), nil, nil
	})
}

// --- Insert-call tool ---

type insertCallInput struct {
	File     string `json:"file" jsonschema:"target Go file"`
	Function string `json:"function" jsonschema:"function identifier: FuncName or Receiver.Method"`
	Call     string `json:"call" jsonschema:"statement to insert, e.g. setupPayOrderRoute(mux, app)"`
	Position string `json:"position,omitempty" jsonschema:"where to insert: before-return (default), end-of-body, or after:<marker>"`
}

func registerInsertCallTool(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "insert_call",
		Description: "Insert a statement into an existing function body at a controlled position — use this instead of update when you only need to add one line inside a function. position values: 'before-return' (default, inserts before the last return), 'end-of-body' (before the closing brace), 'after:<marker>' (after the first line containing <marker>, e.g. 'after:// routes'). Idempotent: if the exact call already exists in the body, it is skipped with a warning.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in insertCallInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		pos := domain.InsertPosition(in.Position)
		if pos == "" {
			pos = domain.InsertBeforeReturn
		}
		result, err := commands.ExecutePlan(ctx, domain.Plan{Actions: []domain.Action{{
			Action:     domain.ActionTypeInsertCall,
			FilePath:   in.File,
			Identifier: in.Function,
			Content:    in.Call,
			Position:   pos,
		}}})
		if err != nil {
			return errorResult(fmt.Sprintf("ERROR (insert_call): %v", err)), nil, nil
		}
		var sb strings.Builder
		for _, w := range result.Warnings {
			fmt.Fprintf(&sb, "WARNING: %s\n", w)
		}
		fmt.Fprintf(&sb, "SUCCESS (insert_call): %d files modified", result.FilesModified)
		return textResult(sb.String()), nil, nil
	})
}

// --- Other tools ---

type executePlanInput struct {
	Plan   string `json:"plan" jsonschema:"plan content with actions to execute (JSON object or YAML string)"`
	Format string `json:"format,omitempty" jsonschema:"plan format: 'json', 'yaml', or omitted for auto-detect (first non-whitespace byte '{' or '[' means JSON, otherwise YAML)"`
}

type implementInput struct {
	Interface string `json:"interface" jsonschema:"fully qualified interface name, e.g. io.ReadCloser or github.com/org/repo/pkg.Interface"`
	Receiver  string `json:"receiver" jsonschema:"receiver type, e.g. *MyStruct"`
	File      string `json:"file" jsonschema:"target file to append stubs to"`
}

type mockInput struct {
	Interface string `json:"interface" jsonschema:"fully qualified interface name"`
	MockName  string `json:"mock_name" jsonschema:"name of the mock struct, e.g. MockBookRepository"`
	File      string `json:"file" jsonschema:"target file to write the mock to"`
}

type testInput struct {
	File       string `json:"file" jsonschema:"target Go file containing the function"`
	Identifier string `json:"identifier" jsonschema:"function or method identifier, e.g. NewApp or Book.Validate"`
}

type tagInput struct {
	File       string `json:"file" jsonschema:"target Go file containing the struct"`
	Identifier string `json:"identifier" jsonschema:"struct identifier"`
	Field      string `json:"field,omitempty" jsonschema:"specific field name to update"`
	Set        string `json:"set,omitempty" jsonschema:"exact tag string to set or append"`
	Auto       string `json:"auto,omitempty" jsonschema:"auto-generate tags for exported fields, e.g. json or bson"`
}

type extractInterfaceInput struct {
	File       string `json:"file" jsonschema:"target Go file containing the struct"`
	Identifier string `json:"identifier" jsonschema:"struct identifier"`
	Name       string `json:"name" jsonschema:"name of the interface to create"`
	Out        string `json:"out,omitempty" jsonschema:"output file path for the interface"`
	MockFile   string `json:"mock_file,omitempty" jsonschema:"generate mock file path"`
	MockName   string `json:"mock_name,omitempty" jsonschema:"name of the mock struct"`
}

// --- Patch tools ---

type patchOpInput struct {
	Op         string `json:"op" jsonschema:"operation: replace, insert_before, insert_after, delete, wrap"`
	Match      string `json:"match,omitempty" jsonschema:"literal text to match inside the function body (whitespace-normalized)"`
	MatchRegex string `json:"match_regex,omitempty" jsonschema:"regex alternative to match; mutually exclusive with match"`
	Occurrence int    `json:"occurrence,omitempty" jsonschema:"1-based index when match appears multiple times; required when ambiguous"`
	Replace    string `json:"replace,omitempty" jsonschema:"replacement text (for replace op)"`
	Code       string `json:"code,omitempty" jsonschema:"line of code to insert (for insert_before / insert_after ops)"`
	Wrap       string `json:"wrap,omitempty" jsonschema:"wrap template with %s as the matched text (for wrap op)"`
}

type patchFunctionInput struct {
	File       string         `json:"file" jsonschema:"target Go file path"`
	Identifier string         `json:"identifier" jsonschema:"function or method identifier, e.g. FuncName or Receiver.Method"`
	Patches    []patchOpInput `json:"patches" jsonschema:"ordered list of patch operations to apply atomically"`
	Preview    bool           `json:"preview,omitempty" jsonschema:"if true, return diff without writing the file"`
}

func registerPatchTools(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "patch_function",
		Description: "Make one or more surgical text-match edits inside a single function body — without rewriting the whole body. " +
			"All patches are scoped to the target function, applied atomically (any failure → nothing written), and goimports runs automatically. " +
			"Use instead of update when changing a few lines inside a function body. " +
			"ops: replace (replace matched text), insert_before (insert a line before the match), insert_after (insert a line after), " +
			"delete (remove the matched text or whole line), wrap (replace match with fmt.Sprintf(wrap, match)). " +
			"match is whitespace-normalized, so indentation does not need to be reproduced exactly. " +
			"When the same text appears more than once, use occurrence (1-based) to disambiguate. " +
			"Use preview=true to get the diff without writing. " +
			"match_regex uses Go's RE2 engine (linear time, no backreferences/lookarounds). For safety, patterns are capped at 1KB, must match 1..1000 times, and cannot be zero-width (^, $, (?:))—narrow the pattern if you hit these limits.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in patchFunctionInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		patches := make([]domain.FunctionPatch, len(in.Patches))
		for i, p := range in.Patches {
			patches[i] = domain.FunctionPatch{
				Op:         domain.PatchOp(p.Op),
				Match:      p.Match,
				MatchRegex: p.MatchRegex,
				Occurrence: p.Occurrence,
				Replace:    p.Replace,
				Code:       p.Code,
				Wrap:       p.Wrap,
			}
		}
		result, err := commands.PatchFunction(ctx, domain.PatchFunctionRequest{
			FilePath:   in.File,
			Identifier: in.Identifier,
			Patches:    patches,
			Preview:    in.Preview,
		})
		if err != nil {
			return errorResult(fmt.Sprintf("ERROR (patch_function): %v", err)), nil, nil
		}
		prefix := fmt.Sprintf("OK: %d patch(es) applied", result.Applied)
		if result.Preview {
			prefix = fmt.Sprintf("PREVIEW: %d patch(es) (not written)", result.Applied)
		}
		if result.Diff != "" {
			return textResult(prefix + "\n\n" + result.Diff), nil, nil
		}
		return textResult(prefix), nil, nil
	})

	registerPatchStructTool(s, commands)
	registerPatchInterfaceTool(s, commands)
}

type structPatchOpInput struct {
	Op       string `json:"op" jsonschema:"operation: add_field, remove_field, rename_field, retype_field, set_tag, set_doc"`
	Name     string `json:"name,omitempty" jsonschema:"target field name (most ops); embed type literal (e.g. io.Reader) for embedded fields"`
	From     string `json:"from,omitempty" jsonschema:"current field name (rename_field only)"`
	To       string `json:"to,omitempty" jsonschema:"new field name (rename_field only)"`
	Type     string `json:"type,omitempty" jsonschema:"field type (add_field / retype_field)"`
	Tag      string `json:"tag,omitempty" jsonschema:"struct tag content without backticks (e.g. json:\"email,omitempty\")"`
	Doc      string `json:"doc,omitempty" jsonschema:"doc comment text (set_doc / add_field); empty string clears the doc"`
	Before   string `json:"before,omitempty" jsonschema:"anchor: insert before this existing field (add_field only)"`
	After    string `json:"after,omitempty" jsonschema:"anchor: insert after this existing field (add_field only)"`
	Position string `json:"position,omitempty" jsonschema:"first or last (add_field only); default is last"`
}

type patchStructInput struct {
	File       string               `json:"file" jsonschema:"target Go file path"`
	Identifier string               `json:"identifier" jsonschema:"struct name, e.g. User or pkg.User"`
	Patches    []structPatchOpInput `json:"patches" jsonschema:"ordered list of patch operations to apply atomically"`
	Preview    bool                 `json:"preview,omitempty" jsonschema:"if true, return diff without writing the file"`
}

func registerPatchStructTool(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "patch_struct",
		Description: "Make granular, atomic edits to a struct's field list — instead of re-emitting the whole declaration via update. " +
			"ops: add_field (name, type, optional tag/doc/before/after/position), remove_field (name), rename_field (from, to), " +
			"retype_field (name, type; preserves tag/doc), set_tag (name, tag; replaces wholesale), set_doc (name, doc). " +
			"Embedded fields are addressed by their bare type name (e.g. name='io.Reader'). " +
			"Any patch failure aborts the whole call and returns the list of current field names as candidates. Use preview=true to get the diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in patchStructInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		patches := make([]domain.StructPatch, len(in.Patches))
		for i, p := range in.Patches {
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
			return errorResult(fmt.Sprintf("ERROR (patch_struct): %v", err)), nil, nil
		}
		prefix := fmt.Sprintf("OK: %d patch(es) applied", result.Applied)
		if result.Preview {
			prefix = fmt.Sprintf("PREVIEW: %d patch(es) (not written)", result.Applied)
		}
		if result.Diff != "" {
			return textResult(prefix + "\n\n" + result.Diff), nil, nil
		}
		return textResult(prefix), nil, nil
	})
}

type interfacePatchOpInput struct {
	Op        string `json:"op" jsonschema:"operation: add_method, remove_method, rename_method, retype_method, set_doc, embed, remove_embed"`
	Name      string `json:"name,omitempty" jsonschema:"target method name (most ops)"`
	From      string `json:"from,omitempty" jsonschema:"current method name (rename_method only)"`
	To        string `json:"to,omitempty" jsonschema:"new method name (rename_method only)"`
	Signature string `json:"signature,omitempty" jsonschema:"full method signature including name, e.g. 'Close() error' or 'Read(p []byte) (int, error)'"`
	Type      string `json:"type,omitempty" jsonschema:"embedded interface type, e.g. 'io.Closer' (embed / remove_embed)"`
	Doc       string `json:"doc,omitempty" jsonschema:"doc comment text (set_doc / add_method)"`
	Before    string `json:"before,omitempty" jsonschema:"anchor: insert before this existing member (add_method / embed)"`
	After     string `json:"after,omitempty" jsonschema:"anchor: insert after this existing member (add_method / embed)"`
	Position  string `json:"position,omitempty" jsonschema:"first or last (add_method / embed); default is last"`
}

type patchInterfaceInput struct {
	File       string                  `json:"file" jsonschema:"target Go file path"`
	Identifier string                  `json:"identifier" jsonschema:"interface name, e.g. Reader or pkg.Reader"`
	Patches    []interfacePatchOpInput `json:"patches" jsonschema:"ordered list of patch operations to apply atomically"`
	Preview    bool                    `json:"preview,omitempty" jsonschema:"if true, return diff without writing the file"`
	MockFile   string                  `json:"mock_file,omitempty" jsonschema:"if set together with mock_name, regenerate this mock when the method set changes"`
	MockName   string                  `json:"mock_name,omitempty" jsonschema:"name of the mock struct to regenerate"`
}

func registerPatchInterfaceTool(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "patch_interface",
		Description: "Make granular, atomic edits to an interface's method list — instead of re-emitting the whole declaration via update_interface. " +
			"Use this INSTEAD of update_interface to add a single method (there is no add_interface_method tool; patch_interface add_method is the equivalent). " +
			"ops: add_method (signature, optional doc/before/after/position), remove_method (name), rename_method (from, to), " +
			"retype_method (name, signature), set_doc (name, doc), embed (type), remove_embed (type). " +
			"When mock_file + mock_name are provided and an op changes the method set, the mock is regenerated automatically (same contract as update_interface). " +
			"Use preview=true to get the diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in patchInterfaceInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		patches := make([]domain.InterfacePatch, len(in.Patches))
		for i, p := range in.Patches {
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
			return errorResult(fmt.Sprintf("ERROR (patch_interface): %v", err)), nil, nil
		}
		prefix := fmt.Sprintf("OK: %d patch(es) applied", result.Applied)
		if result.Preview {
			prefix = fmt.Sprintf("PREVIEW: %d patch(es) (not written)", result.Applied)
		}
		if result.MockUpdated {
			prefix += " (mock regenerated)"
		}
		if result.Diff != "" {
			return textResult(prefix + "\n\n" + result.Diff), nil, nil
		}
		return textResult(prefix), nil, nil
	})
}

func registerOtherTools(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "execute_plan",
		Description: "Execute up to 15 AST edits atomically from a plan — the preferred tool when making several related changes in one step. Accepts JSON (default, auto-detected when content starts with '{' or '[') or YAML; set the 'format' field to force one. JSON shape: {'actions':[{'action':'<type>','file':'path.go','identifier':'Name','content':'...','package':'...','mock_file':'...','mock_name':'...','doc':'...','strip_doc':false,'position':'...','with_test':false}]} (use real double quotes in the payload; single quotes here are just docs). Bool fields (strip_doc, with_test) accept native booleans or the strings 'true'/'false'. Supported actions: create_file, replace_file, add_func, update_func, delete_func, add_struct, update_struct, delete_struct, add_interface, update_interface, delete_interface, insert_call. Content fields must be complete declarations without package declarations or imports; goimports runs after each action.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in executePlanInput) (*mcp.CallToolResult, any, error) {
		plan, err := converters.ToDomainPlan([]byte(in.Plan), in.Format)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to parse plan: %v", err)), nil, nil
		}

		for _, action := range plan.Actions {
			if action.FilePath != "" {
				if err := validateGoFile(action.FilePath); err != nil {
					return err, nil, nil
				}
			}
		}

		result, err := commands.ExecutePlan(ctx, plan)
		if err != nil {
			return errorResult(fmt.Sprintf("plan execution failed: %v", err)), nil, nil
		}

		var sb strings.Builder
		for _, w := range result.Warnings {
			fmt.Fprintf(&sb, "WARNING: %s\n", w)
		}
		fmt.Fprintf(&sb, "SUCCESS: %d files modified", result.FilesModified)
		return textResult(sb.String()), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "implement",
		Description: "Generate method stubs on a struct for any interface it doesn't yet satisfy — use this instead of writing implementations manually with update or create. Works for any interface: local project interfaces (use the fully qualified import path, e.g. github.com/org/repo/internal/pkg.Interface), stdlib (io.ReadCloser), or third-party. Skips methods already implemented. Stubs contain '// TODO(go-surgeon): implement' so you can fill them in afterward. goimports runs automatically.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in implementInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		results, err := commands.Implement(ctx, domain.ImplementRequest{
			Interface: in.Interface,
			Receiver:  in.Receiver,
			FilePath:  in.File,
		})
		if err != nil {
			return errorResult(fmt.Sprintf("failed to implement interface: %v", err)), nil, nil
		}

		if len(results) == 0 {
			return textResult("All methods are already implemented."), nil, nil
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Generated %d missing methods for %s:\n\n", len(results), in.Interface)
		for _, res := range results {
			fmt.Fprintf(&sb, "Symbol: %s\nReceiver: %s\nFile: %s:%d-%d\nCode:\n%s\n\n",
				res.Name, res.Receiver, res.File, res.LineStart, res.LineEnd, res.Code)
		}
		return textResult(sb.String()), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "mock",
		Description: "Generate a standalone function-field mock for any interface without touching the interface file. Use for interfaces you don't own (stdlib, third-party). For interfaces you own, prefer add_interface with mock_file instead. Interface must be fully qualified: e.g. io.Writer or github.com/org/repo/pkg.Interface.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mockInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		result, err := commands.Mock(ctx, domain.MockRequest{
			Interface: in.Interface,
			Receiver:  in.MockName,
			FilePath:  in.File,
		})
		if err != nil {
			return errorResult(fmt.Sprintf("failed to generate mock: %v", err)), nil, nil
		}
		return textResult(result), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "test",
		Description: "Generate a table-driven test skeleton (_test.go file) for a function or method. identifier: 'FuncName' for free functions, 'Type.Method' for methods. The test file is created automatically next to the source file.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in testInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		testFile, err := commands.GenerateTest(ctx, in.File, in.Identifier)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to generate test: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("SUCCESS: Generated test skeleton in %s", testFile)), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "tag",
		Description: "Add or update struct field tags. auto='json' or auto='bson' generates snake_case tags for all exported fields in bulk. Use field+set to update a single specific field's tag. identifier is the struct name.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in tagInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		err := commands.TagStruct(ctx, domain.TagRequest{
			FilePath:   in.File,
			StructName: in.Identifier,
			FieldName:  in.Field,
			SetTag:     in.Set,
			AutoFormat: in.Auto,
		})
		if err != nil {
			return errorResult(fmt.Sprintf("failed to update tags: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("SUCCESS: Updated tags for %s in %s", in.Identifier, in.File)), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "extract_interface",
		Description: "Extract an interface from an existing struct by scanning all its exported methods — useful when refactoring a concrete type to be testable via an interface. Use out to place the interface in a different file (e.g. a domain package). Set mock_file and mock_name to generate the mock in one step.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in extractInterfaceInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		interfaceFile, err := commands.ExtractInterface(ctx, domain.ExtractInterfaceRequest{
			FilePath:      in.File,
			StructName:    in.Identifier,
			InterfaceName: in.Name,
			OutPath:       in.Out,
			MockFile:      in.MockFile,
			MockName:      in.MockName,
		})
		if err != nil {
			return errorResult(fmt.Sprintf("failed to extract interface: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("SUCCESS: Extracted interface %s into %s", in.Name, interfaceFile)), nil, nil
	})
}
