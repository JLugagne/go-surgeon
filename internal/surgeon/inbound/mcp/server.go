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
ALWAYS use go-surgeon tools instead of generic file tools when working on Go projects.

Exploration (replace find/ls/grep/read on Go files):
- graph: explore package structure. Start here on any Go project.
- symbol: read a function, method, or struct body. Use body=true before any edit.
- To read a third-party dependency's source (signatures, internals, usage examples),
  set module='github.com/org/repo' on graph or symbol. Do NOT fall back to find/grep/cat
  inside $GOMODCACHE — the module parameter exists exactly for this purpose.

Editing (replace Edit/Write/Bash on Go files):
- create: add a new file, function, or struct
- update: replace a function, method, struct, or file
- delete: remove a function, method, or struct
- execute_plan: multiple edits in one shot (up to 5 actions, preferred for multi-step changes)

Interface & mock management:
- add_interface / update_interface / delete_interface: manage interfaces and their mocks
- implement: generate stubs for an interface you don't own
- mock: generate a standalone mock for any interface
- extract_interface: extract an interface from an existing struct

Code generation:
- test: generate a table-driven test skeleton
- tag: add or update struct field tags (json, bson, etc.)

Rules that apply to all editing tools:
- Never include package declarations or import blocks in content — goimports runs automatically.
- Always read with symbol body=true before updating or deleting.
- identifier format: FuncName (free function), Receiver.Method (method), StructName (struct).`

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
	registerOtherTools(s, commands)

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
}

type updateInput struct {
	Object     string `json:"object" jsonschema:"what to update: file, func, or struct"`
	File       string `json:"file" jsonschema:"target file path"`
	Identifier string `json:"identifier,omitempty" jsonschema:"AST identifier, e.g. FuncName or Receiver.Method, required for func and struct"`
	Content    string `json:"content" jsonschema:"raw Go source code, no package declaration or imports"`
	Doc        string `json:"doc,omitempty" jsonschema:"set or replace the doc comment (raw text without // prefix)"`
	StripDoc   bool   `json:"strip_doc,omitempty" jsonschema:"remove the existing doc comment"`
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
		Description: "Add a new file (object='file'), free function (object='func'), or struct definition (object='struct') to a Go package. Content is raw Go code — never include package declarations or import blocks, goimports runs automatically and manages all imports. For object='file' the path must not already exist. Prefer execute_plan when creating multiple items together.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in createInput) (*mcp.CallToolResult, any, error) {
		actionType, ok := createObjectMap[in.Object]
		if !ok {
			return errorResult(fmt.Sprintf("invalid object %q: must be file, func, or struct", in.Object)), nil, nil
		}

		result, err := commands.ExecutePlan(ctx, domain.Plan{Actions: []domain.Action{{
			Action:   actionType,
			FilePath: in.File,
			Content:  in.Content,
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
		Description: "Replace an existing function, method, struct, or entire file. For object='func' or 'struct', identifier is required: use 'FuncName' for free functions, 'Receiver.Method' for methods, 'StructName' for structs. Content must be the complete new declaration (full signature and body). Never include package declarations or imports — goimports handles all import changes. Read the current code with symbol body=true first. Doc comments are preserved by default; set doc to replace them or strip_doc=true to remove them.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in updateInput) (*mcp.CallToolResult, any, error) {
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
	MockFile   string `json:"mock_file,omitempty" jsonschema:"target file for the generated mock"`
	MockName   string `json:"mock_name,omitempty" jsonschema:"name of the mock struct"`
	Doc        string `json:"doc,omitempty" jsonschema:"set or replace the doc comment (raw text without // prefix, update only)"`
	StripDoc   bool   `json:"strip_doc,omitempty" jsonschema:"remove the existing doc comment (update only)"`
}

func registerInterfaceTools(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_interface",
		Description: "Add a new interface to a Go file and optionally generate a function-field mock in one step. Use this for interfaces you own (domain ports, repository contracts). Set mock_file and mock_name to atomically create the mock alongside the interface. The generated mock uses func fields (e.g. CreateFunc) with a compile-time interface assertion.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in interfaceInput) (*mcp.CallToolResult, any, error) {
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
		Description: "Update an existing interface and automatically regenerate its mock. Provide mock_file and mock_name to keep the mock in sync with the new signature. Content must be the complete new interface declaration without package declarations or imports. Doc comments are preserved by default; set doc to replace them or strip_doc=true to remove them.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in interfaceInput) (*mcp.CallToolResult, any, error) {
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
		Description: "Delete an interface from a Go file. WARNING: the mock is NOT automatically deleted — you must manually remove the mock file afterward, or the compile-time assertion (var _ I = (*MockI)(nil)) will cause a build error.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in interfaceInput) (*mcp.CallToolResult, any, error) {
		result, err := commands.DeleteInterface(ctx, domain.InterfaceActionRequest{
			FilePath: in.File, Identifier: in.Identifier,
		})
		if err != nil {
			return errorResult(fmt.Sprintf("ERROR (delete_interface): %v", err)), nil, nil
		}
		return textResult(result), nil, nil
	})
}

// --- Other tools ---

type executePlanInput struct {
	Plan string `json:"plan" jsonschema:"YAML plan content with actions to execute"`
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

func registerOtherTools(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "execute_plan",
		Description: "Execute up to 5 AST edits atomically from a YAML plan — the preferred tool when making several related changes in one step. Supported actions: create_file, replace_file, add_func, update_func, delete_func, add_struct, update_struct, delete_struct, add_interface, update_interface, delete_interface. Content fields must be complete declarations without package declarations or imports; goimports runs after each action. Hard limit: 5 actions per plan.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in executePlanInput) (*mcp.CallToolResult, any, error) {
		plan, err := converters.ToDomainPlan([]byte(in.Plan))
		if err != nil {
			return errorResult(fmt.Sprintf("failed to parse plan: %v", err)), nil, nil
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
		Description: "Generate TODO stub methods on a struct for an interface it doesn't yet satisfy. Use this for interfaces you don't own: stdlib (io.ReadCloser), third-party (github.com/pkg.Interface), or local (github.com/org/repo/internal/pkg.Interface). Skips methods already implemented. Stubs contain '// TODO: implement' and panic. goimports runs automatically.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in implementInput) (*mcp.CallToolResult, any, error) {
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
