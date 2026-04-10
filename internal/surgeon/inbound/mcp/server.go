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

// NewServer creates an MCP server with all go-surgeon tools registered.
func NewServer(commands service.SurgeonCommands, queries service.SurgeonQueries) *mcp.Server {
	s := mcp.NewServer(
		&mcp.Implementation{
			Name:    "go-surgeon",
			Version: "1.0.0",
		},
		nil,
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
}

type symbolInput struct {
	Query string `json:"query" jsonschema:"symbol name to search for, supports Name or Receiver.Method or pkg.Name forms"`
	Body  bool   `json:"body,omitempty" jsonschema:"show the full function or struct body with line numbers"`
	Tests bool   `json:"tests,omitempty" jsonschema:"include test files in the search"`
	Dir   string `json:"dir,omitempty" jsonschema:"directory to search in, defaults to current directory"`
}

func registerQueryTools(s *mcp.Server, queries service.SurgeonQueries) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "graph",
		Description: "Print the package graph of the current Go project. Lists packages, symbols, summaries, and dependencies.",
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
		Description: "Look up a symbol (function, method, or struct) by name in Go source files.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in symbolInput) (*mcp.CallToolResult, any, error) {
		dir := in.Dir
		if dir == "" {
			dir = "."
		}

		results := findSymbols(ctx, queries, in.Query, in.Tests, dir)
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
		Description: "Create a new file, function, or struct. Set object to 'file', 'func', or 'struct'.",
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
		Description: "Update an existing file, function, or struct. Set object to 'file', 'func', or 'struct'. Identifier is required for func and struct.",
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
		Description: "Delete a function or struct from a Go file. Set object to 'func' or 'struct'.",
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
}

func registerInterfaceTools(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_interface",
		Description: "Add a new interface to a Go file with optional auto-mock generation",
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
		Description: "Update an existing interface in a Go file, regenerates mock automatically",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in interfaceInput) (*mcp.CallToolResult, any, error) {
		result, err := commands.UpdateInterface(ctx, domain.InterfaceActionRequest{
			FilePath: in.File, Identifier: in.Identifier, Content: in.Content,
			MockFile: in.MockFile, MockName: in.MockName,
		})
		if err != nil {
			return errorResult(fmt.Sprintf("ERROR (update_interface): %v", err)), nil, nil
		}
		return textResult(result), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_interface",
		Description: "Delete an interface from a Go file",
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
		Description: "Execute a YAML plan containing multiple actions in order. Supports all action types: create_file, replace_file, add_func, update_func, delete_func, add_struct, update_struct, delete_struct, add_interface, update_interface, delete_interface.",
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
		Description: "Generate missing interface method stubs on a struct. Resolves interfaces from stdlib, third-party, and project-local packages.",
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
		Description: "Generate a function-field mock for any interface (stdlib, third-party, or project-local).",
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
		Description: "Generate a table-driven test skeleton for a function or method.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in testInput) (*mcp.CallToolResult, any, error) {
		testFile, err := commands.GenerateTest(ctx, in.File, in.Identifier)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to generate test: %v", err)), nil, nil
		}
		return textResult(fmt.Sprintf("SUCCESS: Generated test skeleton in %s", testFile)), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "tag",
		Description: "Manipulate struct tags. Auto-generate json/bson tags or set exact tags on specific fields.",
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
		Description: "Extract an interface from a struct by scanning all its exported methods. Optionally generates a mock.",
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
