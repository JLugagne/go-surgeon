package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const serverInstructions = `go-surgeon is the AST-aware editor for Go files. It replaces Read/Edit/Write/Grep/Glob/Bash for anything that touches a .go file — use it for reading Go code too, not just editing. This applies end-to-end: don't start with go-surgeon and then fall back to Grep mid-task.

The mental model has three layers:

EXPLORE (before you edit, to understand what's there)
- overview: list packages and symbols across the project. START HERE on an unfamiliar codebase — one call shows the package tree + (with symbols=true) per-file signatures. Also use when entering a new package for the first time: overview focus=pkg/path symbols=true shows every type/func/interface in one call, saving 5-10 individual symbol calls.
- symbol: read one declaration. Two modes:
    - exact (query): fetch a known function/method/type/var/const. Set body=true to see the implementation — do this before every edit. body=true also returns the file's package line and import block for free. When exploring an unfamiliar file, always set context=file: you get the symbol's body plus an outline of every sibling declaration in one call, saving 5+ follow-up symbol calls.
    - regex (pattern): list every declaration whose name matches. Use instead of Grep for discovery: it matches only declarations, so you don't wade through usages. Covers funcs, methods, types, vars, and consts.
- Both accept module='github.com/org/repo' to look inside a dependency's source instead of the current project. Use this rather than find/cat inside $GOMODCACHE.

EDIT (pick the narrowest tool that fits — bigger tools aren't safer, they rewrite more)
- Changing a few lines inside one function body      → patch_function
- Same rename across many functions in one file      → patch_file (bulk text substitution with AST safety)
- Editing the VALUE of a top-level const or var      → patch_decl (multi-line string const, error var, etc.)
- Single field change on a struct                    → patch_struct
- Single method change on an interface               → patch_interface (regenerates the mock too)
- Inserting one statement at a fixed position        → insert_call
- Whole-declaration replacement (func/struct/file)   → update
- Adding a brand-new declaration (func/struct/file)  → create
- Removing a declaration                             → delete
- Adding an interface WITH its mock in one step      → add_interface (set mock_file + mock_name)
- Updating or deleting an interface                  → update_interface / delete_interface (keep the mock in sync via mock_file/mock_name/delete_mock)
- Several coordinated edits                          → execute_plan (atomic, up to 15 actions)

Why the granular tools matter: re-emitting a whole function or struct via update forces you to reproduce the entire body, which is a common source of subtle drift (lost comments, reordered fields, missed branches). patch_function/patch_decl/patch_struct/patch_interface edit in place and preserve everything you didn't explicitly change.

VALIDATE (after you edit, to confirm the change is sound)
- build_check: runs 'go build' scoped to a package or directory (default './...') and returns structured diagnostics (file, line, column, message) deduplicated per file. Call this after any edit that could affect compilation instead of asking the user to run 'go build' or shelling out. Set tests=true to also compile test files. timeout_seconds caps the run (default 60, max 600). 'go vet' is out of scope; use build_check only for compile errors.
- test_run: runs 'go test' for a package/directory and reports pass/fail plus failing test output.

INTERFACE WORKFLOWS
- To add ONE method to an existing interface: use patch_interface add_method. There is no add_interface_method tool, and update_interface is overkill here.
- To restructure an interface significantly: update_interface with the complete new declaration.
- implement: generate method stubs on a struct for an interface it doesn't yet satisfy.
- mock: generate a standalone mock for an interface you don't own (stdlib/third-party). For interfaces you own, prefer add_interface + mock_file.
- extract_interface: derive an interface from an existing struct's exported methods.

CODE GENERATION
- test: generate a table-driven test skeleton for a function or method.
- tag: bulk-generate or set struct field tags (json, bson, …).

VALIDATE (after editing, before declaring the task done)
- test_run: run 'go test' scoped to a package/directory and get a compact pass/fail report with per-test timing and failure file:line references. Prefer this over shelling out to go test yourself. Pair with build_check for compile-time validation.

UNIVERSAL RULES
- content is raw Go code: never include 'package ...' or 'import ...' blocks — goimports runs after every edit and manages imports.
- Always read with symbol body=true before update/delete — it's cheap and it prevents the "I replaced the wrong thing" class of bug.
- identifier forms: 'FuncName' (free function), 'Receiver.Method' (method), 'StructName' / 'InterfaceName' (types), 'pkg.Name' (package-qualified when ambiguous).`

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

	s.AddReceivingMiddleware(schemaHintMiddleware())

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

type buildCheckInput struct {
	Dir            string `json:"dir,omitempty" jsonschema:"relative directory or package pattern to build, defaults to './...'. Absolute paths and '..' traversal are rejected."`
	Tests          bool   `json:"tests,omitempty" jsonschema:"compile test files too (uses 'go test -count=0 -run ^$' instead of 'go build')"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"timeout in seconds, default 60, max 600"`
}

type symbolInput struct {
	Query       string `json:"query,omitempty" jsonschema:"exact symbol name: Name, Receiver.Method, or pkg.Name. Mutually exclusive with pattern."`
	Pattern     string `json:"pattern,omitempty" jsonschema:"regex to match against declaration names (funcs, methods, types); returns the list of matches. Mutually exclusive with query."`
	Body        bool   `json:"body,omitempty" jsonschema:"show the full function or struct body with line numbers"`
	Tests       bool   `json:"tests,omitempty" jsonschema:"include test files in the search"`
	Dir         string `json:"dir,omitempty" jsonschema:"directory to search in, defaults to current directory"`
	Module      string `json:"module,omitempty" jsonschema:"import path of a dependency to search in instead of the current project, e.g. 'github.com/spf13/cobra'"`
	Context     string `json:"context,omitempty" jsonschema:"optional 'file' to additionally return an outline of sibling declarations in the same file (signatures only, with line ranges) — set this when you might edit nearby code and want to see the file's shape in one call"`
	TokenBudget int    `json:"token_budget,omitempty" jsonschema:"approximate max tokens in output (pattern mode), 0 means unlimited"`
}

func registerQueryTools(s *mcp.Server, queries service.SurgeonQueries) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "overview",
		Description: "List packages and symbols across the project. START HERE on an unfamiliar codebase. Also use when entering a new package: focus=pkg/path symbols=true shows every type/func/interface in one call. Explore dependencies with module='github.com/org/repo'. token_budget caps output on large projects.",
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
		Description: "Read one declaration (exact query='Name'/'Receiver.Method'/'pkg.Name') or list matches (pattern=regex). Indexes funcs, methods, types, vars, and consts. body=true shows the implementation plus the file's package line and import block. When exploring an unfamiliar file, use context=file to also get an outline of every sibling declaration — saves 5+ follow-up calls. Works on dependencies via module='github.com/org/repo'. query and pattern are mutually exclusive.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in symbolInput) (*mcp.CallToolResult, any, error) {
		dir := in.Dir
		if dir == "" {
			dir = "."
		}

		if in.Query != "" && in.Pattern != "" {
			return errorResult("query and pattern are mutually exclusive — set one"), nil, nil
		}
		if in.Query == "" && in.Pattern == "" {
			return errorResult("one of query or pattern is required"), nil, nil
		}

		if in.Pattern != "" {
			results, err := queries.FindSymbols(ctx, domain.SymbolQuery{Pattern: in.Pattern, Tests: in.Tests, Module: in.Module, Context: in.Context}, dir)
			if err != nil {
				return errorResult(err.Error()), nil, nil
			}
			if len(results) == 0 {
				return textResult(fmt.Sprintf("No declarations match pattern %q.", in.Pattern)), nil, nil
			}
			if in.Body && len(results) > 3 {
				return errorResult(fmt.Sprintf("body=true refused: pattern matched %d declarations (max 3). Refine the pattern or re-run per-symbol with query.", len(results))), nil, nil
			}
			text := formatPatternResults(results, in.Body, in.Pattern, in.TokenBudget)
			return textResult(text), nil, nil
		}

		results := findSymbols(ctx, queries, in.Query, in.Tests, dir, in.Module, in.Context)
		if len(results) == 0 {
			return textResult(fmt.Sprintf("No matches found for '%s'.\nHint: run 'overview' with symbols=true and dir set to list available symbols.", in.Query)), nil, nil
		}

		text := formatSymbolResults(results, in.Body, in.Query)
		return textResult(text), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "build_check",
		Description: "Run `go build` against a package/directory and return structured compile diagnostics. Use this AFTER editing Go code to verify the package still compiles — diagnostics carry file:line:column + message, deduplicated per file. Set tests=true to also compile _test.go files. dir defaults to './...' (the whole module); pass a relative path like 'internal/foo' or 'internal/foo/...' to scope the check. timeout_seconds defaults to 60 (max 600). `go vet` is out of scope — this tool only reports errors the compiler itself sees.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in buildCheckInput) (*mcp.CallToolResult, any, error) {
		result, err := queries.BuildCheck(ctx, domain.BuildCheckRequest{
			Dir:            in.Dir,
			Tests:          in.Tests,
			TimeoutSeconds: in.TimeoutSeconds,
		})
		if err != nil {
			return errorResult(fmt.Sprintf("build_check failed: %v", err)), nil, nil
		}

		text := formatBuildCheckResult(result)
		res := textResult(text)
		res.StructuredContent = buildCheckOutput{
			Success:     result.Success,
			Diagnostics: convertBuildDiagnostics(result.Diagnostics),
			RawOutput:   result.RawOutput,
			ExitCode:    result.ExitCode,
			DurationMs:  result.DurationMs,
			TimedOut:    result.TimedOut,
			Truncated:   result.Truncated,
		}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "test_run",
		Description: "Run `go test` scoped to a package/directory and return a compact pass/fail report with per-test timing and failure file:line references. Use after editing Go code to verify behavior in-loop. dir defaults to ./..., timeout defaults to 120s (max 600).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in testRunInput) (*mcp.CallToolResult, any, error) {
		result, err := queries.TestRun(ctx, domain.TestRunRequest{
			Dir:            in.Dir,
			Run:            in.Run,
			Count:          in.Count,
			Race:           in.Race,
			Tags:           in.Tags,
			TimeoutSeconds: in.TimeoutSeconds,
		})
		if err != nil {
			return errorResult(fmt.Sprintf("ERROR (test_run): %v", err)), nil, nil
		}
		res := textResult(formatTestRunResult(result))
		res.StructuredContent = result
		return res, nil, nil
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
		Description: "Add a new file, function, or struct. object='file' creates a new file; 'func'/'struct' append to an existing file. When adding several related items, use execute_plan instead.",
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
		res := textResult(sb.String())
		res.StructuredContent = editOutput{
			FilesModified: result.Files,
			Symbols:       []symbolEdit{{Action: string(actionType), File: in.File}},
			Warnings:      result.Warnings,
			AddedImports:  result.AddedImports,
		}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update",
		Description: "Replace a whole function, method, struct, or file. For small changes inside a function body, prefer patch_function. content is the complete new declaration (signature + body). Doc comments are kept unless you set doc or strip_doc=true.",
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
		res := textResult(sb.String())
		res.StructuredContent = editOutput{
			FilesModified: result.Files,
			Symbols:       []symbolEdit{{Action: string(actionType), Identifier: in.Identifier, File: in.File}},
			Warnings:      result.Warnings,
			AddedImports:  result.AddedImports,
		}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete",
		Description: "Remove a function, method, or struct. object='struct' also removes every method on the struct across the package. For interfaces, use delete_interface instead (handles mock cleanup).",
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
		res := textResult(sb.String())
		res.StructuredContent = editOutput{
			FilesModified: result.Files,
			Symbols:       []symbolEdit{{Action: string(actionType), Identifier: in.Identifier, File: in.File}},
			Warnings:      result.Warnings,
			AddedImports:  result.AddedImports,
		}
		return res, nil, nil
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
		Description: "Add a new interface and its mock atomically. Set mock_file + mock_name to generate a function-field mock with a compile-time var assertion. Prefer this over create for interfaces.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in interfaceInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		result, addedImports, err := commands.AddInterface(ctx, domain.InterfaceActionRequest{
			FilePath: in.File, Identifier: in.Identifier, Content: in.Content,
			MockFile: in.MockFile, MockName: in.MockName,
		})
		if err != nil {
			return errorResult(fmt.Sprintf("ERROR (add_interface): %v", err)), nil, nil
		}
		files := []string{in.File}
		if in.MockFile != "" {
			files = append(files, in.MockFile)
		}
		res := textResult(result)
		res.StructuredContent = editOutput{
			FilesModified: files,
			Symbols:       []symbolEdit{{Action: "add_interface", Identifier: in.Identifier, File: in.File}},
			AddedImports:  addedImports,
		}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_interface",
		Description: "Replace an interface wholesale and regenerate its mock. For single-method changes, prefer patch_interface. Pass mock_file + mock_name to keep the mock in sync. Doc comments are kept unless you set doc or strip_doc=true.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in interfaceInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		result, addedImports, err := commands.UpdateInterface(ctx, domain.InterfaceActionRequest{
			FilePath: in.File, Identifier: in.Identifier, Content: in.Content,
			MockFile: in.MockFile, MockName: in.MockName,
			Doc: in.Doc, StripDoc: in.StripDoc,
		})
		if err != nil {
			return errorResult(fmt.Sprintf("ERROR (update_interface): %v", err)), nil, nil
		}
		files := []string{in.File}
		if in.MockFile != "" {
			files = append(files, in.MockFile)
		}
		res := textResult(result)
		res.StructuredContent = editOutput{
			FilesModified: files,
			Symbols:       []symbolEdit{{Action: "update_interface", Identifier: in.Identifier, File: in.File}},
			AddedImports:  addedImports,
		}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_interface",
		Description: "Delete an interface. Pass delete_mock=true with mock_file + mock_name to also remove the mock struct, its methods, and the var assertion. The mock file is kept even if empty.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in interfaceInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		if in.MockFile != "" {
			if err := validateGoFile(in.MockFile); err != nil {
				return err, nil, nil
			}
		}
		result, addedImports, err := commands.DeleteInterface(ctx, domain.InterfaceActionRequest{
			FilePath:   in.File,
			Identifier: in.Identifier,
			MockFile:   in.MockFile,
			MockName:   in.MockName,
			DeleteMock: in.DeleteMock,
		})
		if err != nil {
			return errorResult(fmt.Sprintf("ERROR (delete_interface): %v", err)), nil, nil
		}
		files := []string{in.File}
		if in.DeleteMock && in.MockFile != "" {
			files = append(files, in.MockFile)
		}
		res := textResult(result)
		res.StructuredContent = editOutput{
			FilesModified: files,
			Symbols:       []symbolEdit{{Action: "delete_interface", Identifier: in.Identifier, File: in.File}},
			AddedImports:  addedImports,
		}
		return res, nil, nil
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
		Description: "Insert one statement into a function body. Idempotent: skipped if already present. position: 'before-return' (default), 'end-of-body', or 'after:<marker>'.",
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
		res := textResult(sb.String())
		res.StructuredContent = editOutput{
			FilesModified: result.Files,
			Symbols:       []symbolEdit{{Action: "insert_call", Identifier: in.Function, File: in.File}},
			Warnings:      result.Warnings,
			AddedImports:  result.AddedImports,
		}
		return res, nil, nil
	})
}

// --- Other tools ---

type executePlanInput struct {
	Actions []executePlanActionInput `json:"actions" jsonschema:"ordered list of AST actions to execute atomically (up to 15)"`
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
	AtLine     int    `json:"at_line,omitempty" jsonschema:"target a single line by file-absolute line number (1-based, matches symbol body=true output). Mutually exclusive with match/match_regex."`
	FromLine   int    `json:"from_line,omitempty" jsonschema:"first line of a file-absolute line range (1-based, inclusive). Pair with to_line."`
	ToLine     int    `json:"to_line,omitempty" jsonschema:"last line of a file-absolute line range (1-based, inclusive). Must be >= from_line."`
	Params     string `json:"params,omitempty" jsonschema:"for set_signature: new parameter list including parens, e.g. '(ctx context.Context, x int)'. Empty keeps the current params."`
	Returns    string `json:"returns,omitempty" jsonschema:"for set_signature: new result list, e.g. 'error' or '([]byte, error)'. Empty keeps the current returns."`
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
		Description: "Edit lines inside one function body by matching on text. Patches are scoped to the named function and applied atomically. " +
			"ops: replace, insert_before, insert_after, delete, wrap, set_signature. match is whitespace-normalized. Disambiguate with occurrence (1-based). " +
			"SIGNATURE EDITS: set_signature rewrites only the params list and/or the results list of the function or method, leaving the body, name, receiver, and any generic type parameters intact. Supply params (with parens) and/or returns; at least one is required. " +
			"LINE TARGETING: use at_line or from_line/to_line with file-absolute line numbers (from symbol body=true) instead of text matching — faster and unambiguous. Mutually exclusive with match/match_regex. " +
			"match_regex: RE2 alternative to match (no backrefs/lookarounds). " +
			"preview=true returns diff without writing.",
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
				AtLine:     p.AtLine,
				FromLine:   p.FromLine,
				ToLine:     p.ToLine,
				Params:     p.Params,
				Returns:    p.Returns,
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
	})

	registerPatchStructTool(s, commands)
	registerPatchInterfaceTool(s, commands)
	registerPatchFileTool(s, commands)
	registerPatchDeclTool(s, commands)
}

// --- patch_file ---

type filePatchOpInput struct {
	Match      string `json:"match,omitempty" jsonschema:"literal text; all occurrences are replaced. Mutually exclusive with match_regex."`
	MatchRegex string `json:"match_regex,omitempty" jsonschema:"RE2 regex alternative to match; use $1, $2, ... in replace for submatch substitution. Mutually exclusive with match."`
	Replace    string `json:"replace" jsonschema:"replacement text; supports $1/$2/... when match_regex is used"`
}

type patchFileInput struct {
	File    string             `json:"file" jsonschema:"target Go file path"`
	Patches []filePatchOpInput `json:"patches" jsonschema:"ordered list of substitutions; each patch sees the result of the previous one"`
	Preview bool               `json:"preview,omitempty" jsonschema:"if true, return diff without writing the file"`
}

func registerPatchFileTool(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "patch_file",
		Description: "Whole-file text substitution with AST safety — for cross-function batch edits (e.g. renaming a literal across many test functions). Each patch uses match (literal, all occurrences) or match_regex (RE2 with $1/$2 backrefs in replace). Patches apply sequentially; each sees the result of the previous one. After substitution the file is re-parsed and gofmt'd; if the result is invalid Go the patch is rejected and nothing is written. Zero-match patches are allowed and recorded as warnings. Complements patch_function (per-function) — prefer patch_function when edits are scoped to one body.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in patchFileInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		patches := make([]domain.FilePatch, len(in.Patches))
		for i, p := range in.Patches {
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
		})
		if err != nil {
			return errorResult(fmt.Sprintf("ERROR (patch_file): %v", err)), nil, nil
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
	})
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
		Name:        "patch_struct",
		Description: "Edit a struct's field list: add_field, remove_field, rename_field, retype_field, set_tag, set_doc. Patches apply atomically. Embedded fields use their type name (e.g. name='io.Reader'). preview=true returns diff without writing.",
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
		Name:        "patch_interface",
		Description: "Edit an interface's method list and regenerate the mock. ops: add_method, remove_method, rename_method, retype_method, set_doc, embed, remove_embed. Pass mock_file + mock_name for automatic mock regeneration. preview=true returns diff without writing.",
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
	})
}

func registerOtherTools(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "execute_plan",
		Description: "Apply up to 15 related AST edits atomically — any failure rolls everything back. " +
			"Actions: create_file, replace_file, add_func, update_func, delete_func, add_struct, update_struct, delete_struct, add_interface, update_interface, delete_interface, insert_call.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in executePlanInput) (*mcp.CallToolResult, any, error) {
		actions := make([]domain.Action, len(in.Actions))
		for i, a := range in.Actions {
			actions[i] = domain.Action{
				Action:      domain.ActionType(a.Action),
				FilePath:    a.File,
				PackagePath: a.Package,
				Identifier:  a.Identifier,
				Content:     a.Content,
				MockFile:    a.MockFile,
				MockName:    a.MockName,
				Doc:         a.Doc,
				StripDoc:    a.StripDoc,
				Position:    domain.InsertPosition(a.Position),
				WithTest:    a.WithTest,
			}
			if actions[i].FilePath != "" {
				if err := validateGoFile(actions[i].FilePath); err != nil {
					return err, nil, nil
				}
			}
		}
		plan := domain.Plan{Actions: actions}

		result, err := commands.ExecutePlan(ctx, plan)
		if err != nil {
			return errorResult(fmt.Sprintf("plan execution failed: %v", err)), nil, nil
		}

		var sb strings.Builder
		for _, w := range result.Warnings {
			fmt.Fprintf(&sb, "WARNING: %s\n", w)
		}
		fmt.Fprintf(&sb, "SUCCESS: %d files modified", result.FilesModified)
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
		}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "implement",
		Description: "Generate method stubs for a struct to satisfy an interface. Already-implemented methods are skipped. Stubs are marked '// TODO(go-surgeon): implement'. Interface must be fully qualified (e.g. io.ReadCloser, github.com/org/repo/pkg.Interface).",
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
			res := textResult("All methods are already implemented.")
			res.StructuredContent = implementOutput{File: in.File, Interface: in.Interface, Receiver: in.Receiver, Stubs: []string{}}
			return res, nil, nil
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Generated %d missing methods for %s:\n\n", len(results), in.Interface)
		for _, res := range results {
			fmt.Fprintf(&sb, "Symbol: %s\nReceiver: %s\nFile: %s:%d-%d\nCode:\n%s\n\n",
				res.Name, res.Receiver, res.File, res.LineStart, res.LineEnd, res.Code)
		}
		stubs := make([]string, len(results))
		for i, r := range results {
			stubs[i] = r.Name
		}
		res := textResult(sb.String())
		res.StructuredContent = implementOutput{File: in.File, Interface: in.Interface, Receiver: in.Receiver, Stubs: stubs}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "mock",
		Description: "Generate a function-field mock for an interface you don't own (stdlib, third-party). For your own interfaces, use add_interface with mock_file instead. Interface must be fully qualified.",
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
		res := textResult(result)
		res.StructuredContent = mockOutput{File: in.File, Interface: in.Interface, MockName: in.MockName}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "test",
		Description: "Scaffold a table-driven _test.go for a function or method. Handles boilerplate (t.Run loop, tt struct, receiver setup). identifier: 'FuncName' or 'Type.Method'.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in testInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		testFile, err := commands.GenerateTest(ctx, in.File, in.Identifier)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to generate test: %v", err)), nil, nil
		}
		res := textResult(fmt.Sprintf("SUCCESS: Generated test skeleton in %s", testFile))
		res.StructuredContent = testOutput{TestFile: testFile, Identifier: in.Identifier}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "tag",
		Description: "Manage struct field tags. Bulk: auto='json'/'bson' generates snake_case tags on all exported fields. Targeted: field + set updates one field's tag. patch_struct set_tag is an alternative for single fields.",
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
		res := textResult(fmt.Sprintf("SUCCESS: Updated tags for %s in %s", in.Identifier, in.File))
		res.StructuredContent = tagOutput{File: in.File, Identifier: in.Identifier, Field: in.Field}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "extract_interface",
		Description: "Derive an interface from a struct's exported methods. Use out to place it in a specific file. Set mock_file + mock_name to generate the mock in the same step.",
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
		res := textResult(fmt.Sprintf("SUCCESS: Extracted interface %s into %s", in.Name, interfaceFile))
		res.StructuredContent = extractInterfaceOutput{InterfaceName: in.Name, InterfaceFile: interfaceFile, MockFile: in.MockFile, MockName: in.MockName}
		return res, nil, nil
	})
}

type executePlanActionInput struct {
	Action     string `json:"action" jsonschema:"action type: create_file, replace_file, add_func, update_func, delete_func, add_struct, update_struct, delete_struct, add_interface, update_interface, delete_interface, insert_call"`
	File       string `json:"file" jsonschema:"target file path"`
	Package    string `json:"package,omitempty" jsonschema:"package import path (for cross-package operations)"`
	Identifier string `json:"identifier,omitempty" jsonschema:"AST identifier, e.g. FuncName or Receiver.Method"`
	Content    string `json:"content,omitempty" jsonschema:"raw Go source code, no package declaration or imports"`
	MockFile   string `json:"mock_file,omitempty" jsonschema:"target file for the generated mock"`
	MockName   string `json:"mock_name,omitempty" jsonschema:"name of the mock struct"`
	Doc        string `json:"doc,omitempty" jsonschema:"set or replace the doc comment (raw text without // prefix)"`
	StripDoc   bool   `json:"strip_doc,omitempty" jsonschema:"remove the existing doc comment"`
	Position   string `json:"position,omitempty" jsonschema:"insert position: before-return, end-of-body, or after:<marker>"`
	WithTest   bool   `json:"with_test,omitempty" jsonschema:"generate a test skeleton alongside the function"`
}

type testRunInput struct {
	Dir            string `json:"dir,omitempty" jsonschema:"directory to test (relative to the project root). Defaults to ./..."`
	Run            string `json:"run,omitempty" jsonschema:"optional -run regexp filter"`
	Count          int    `json:"count,omitempty" jsonschema:"iterations per test (default 1)"`
	Race           bool   `json:"race,omitempty" jsonschema:"enable the race detector"`
	Tags           string `json:"tags,omitempty" jsonschema:"build tags (whitelist [a-z_][a-z0-9_,.]*)"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"overall timeout in seconds (default 120, max 600)"`
}

type patchDeclInput struct {
	File       string         `json:"file" jsonschema:"target Go file path"`
	Identifier string         `json:"identifier" jsonschema:"top-level const or var identifier, e.g. serverInstructions or ErrNotFound"`
	Patches    []patchOpInput `json:"patches" jsonschema:"ordered list of patch operations to apply atomically"`
	Preview    bool           `json:"preview,omitempty" jsonschema:"if true, return diff without writing the file"`
}

func registerPatchDeclTool(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "patch_decl",
		Description: "Edit the VALUE of a top-level const or var declaration by matching on text — the const/var equivalent of patch_function. " +
			"Targets the value expression of the named identifier (works on grouped const/var blocks too: identifier=\"B\" in `const (A=1; B=2)` picks B). " +
			"For a single string-literal value (including multi-line backtick raw strings), matches apply to the text INSIDE the quotes/backticks — delimiters are preserved automatically. " +
			"For any other value expression (composite literal, function call, number, ...), matches apply to the full value text as it appears in source. " +
			"ops: replace, insert_before, insert_after, delete, wrap. match is whitespace-normalized. Disambiguate with occurrence (1-based). " +
			"LINE TARGETING: use at_line or from_line/to_line with file-absolute line numbers (from symbol body=true) instead of text matching — faster and unambiguous. Mutually exclusive with match/match_regex. " +
			"match_regex: RE2 alternative to match (no backrefs/lookarounds). " +
			"Typed vars without an initializer (var x int) cannot be patched — use update to add a value. " +
			"preview=true returns diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in patchDeclInput) (*mcp.CallToolResult, any, error) {
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
			return errorResult(fmt.Sprintf("ERROR (patch_decl): %v", err)), nil, nil
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
	})
}
