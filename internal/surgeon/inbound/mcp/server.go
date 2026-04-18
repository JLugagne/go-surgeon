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

WHEN TO BATCH WITH execute_plan
Principle: if you're about to make 3+ related edits that must land together, use execute_plan — one atomic call with rollback on failure.
- Example A (same change to two interfaces): bundle two patch_interface actions in one execute_plan (patch_interface actions carry their ops via patch_interface_ops). This is two round-trips as separate calls AND if the second fails the file is left with only one interface updated; the bundled form rolls both back on failure.
- Example B (new interface + implementation + test stub): one execute_plan with add_interface + add_struct (or create_file for the impl) + create_file for the _test.go lands the whole vertical slice atomically; a partial failure rolls back, so you never commit a half-wired type.
When NOT to use it: single-object edits don't need it. Don't reach for execute_plan just to feel safer — the granular patch_* tools already preserve everything you didn't touch.

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

ERROR HINTS
- The patch_* tools ('patches' field) are guarded by a pre-validation hint. If a client accidentally sends 'patches' as a JSON-encoded string instead of an array, you'll get an explicit ERROR message naming the cause ('JSON-encoded string instead of an array', and 'serialized twice' when the inner string itself parses as an array) before the SDK's opaque schema error fires. When you see that message: resend 'patches' as a raw JSON array (not a stringified one), or fall back to update / update_interface / update_struct with the full replacement declaration.
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
	registerReferencesTools(s, queries)
	registerBatchQueryTool(s, queries)
	registerRenameTool(s, commands)
	registerDescribeTool(s)

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
	AffectedBy     string `json:"affected_by,omitempty" jsonschema:"path to a .go file — narrow the build to the package that owns this file plus every package in the module that (transitively) imports it. Mutually exclusive with dir. Great after editing one file in a large monorepo — skips rebuilding unrelated packages."`
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
	Outline     bool   `json:"outline,omitempty" jsonschema:"pattern mode only: include first-sentence doc summary per match (middle ground between signature-only and body=true)"`
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
			return errorResultWithCode(fmt.Sprintf("failed to build graph: %v", err), err), nil, nil
		}

		text := formatGraph(packages, opts)
		return textResult(text), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "symbol",
		Description: "Read one declaration (exact query='Name'/'Receiver.Method'/'pkg.Name') or list matches (pattern=regex). Indexes funcs, methods, types, vars, and consts. body=true shows the implementation plus the file's package line and import block. In pattern mode, outline=true returns signature + first-sentence doc summary per match (middle ground between signature-only and body=true). When exploring an unfamiliar file, use context=file to also get an outline of every sibling declaration — saves 5+ follow-up calls. Works on dependencies via module='github.com/org/repo'. query and pattern are mutually exclusive.",
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
				return errorResultWithCode(err.Error(), err), nil, nil
			}
			if len(results) == 0 {
				return textResult(fmt.Sprintf("No declarations match pattern %q.", in.Pattern)), nil, nil
			}
			text := formatPatternResults(results, in.Body, in.Pattern, in.TokenBudget, in.Outline)
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
			AffectedBy:     in.AffectedBy,
		})
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("build_check failed: %v", err), err), nil, nil
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
		Description: "Run `go test` scoped to a package/directory and return a compact pass/fail report with per-test timing and failure file:line references. Use after editing Go code to verify behavior in-loop. dir defaults to ./..., timeout defaults to 120s (max 600). Pass affected_by=path/to/file.go to run only the owning package plus its reverse-dependency closure (mutually exclusive with dir).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in testRunInput) (*mcp.CallToolResult, any, error) {
		result, err := queries.TestRun(ctx, domain.TestRunRequest{
			Dir:            in.Dir,
			Run:            in.Run,
			Count:          in.Count,
			Race:           in.Race,
			Tags:           in.Tags,
			TimeoutSeconds: in.TimeoutSeconds,
			AffectedBy:     in.AffectedBy,
		})
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (test_run): %v", err), err), nil, nil
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
	Identifier string `json:"identifier" jsonschema:"AST identifier, e.g. FuncName or Receiver.Method"`
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
		Description: "Replace a whole function, method, struct, or file. For small changes inside a function body, prefer patch_function. content is the complete new declaration (signature + body). Doc comments are kept unless you set doc or strip_doc=true. preview=true returns a unified diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in updateInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		actionType, ok := updateObjectMap[in.Object]
		if !ok {
			return errorResult(fmt.Sprintf("invalid object %q: must be file, func, or struct", in.Object)), nil, nil
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
		Description: "Remove a function, method, or struct. object='struct' also removes every method on the struct across the package. For interfaces, use delete_interface instead (handles mock cleanup). preview=true returns a unified diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in deleteInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		actionType, ok := deleteObjectMap[in.Object]
		if !ok {
			return errorResult(fmt.Sprintf("invalid object %q: must be func or struct", in.Object)), nil, nil
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
	Preview    bool   `json:"preview,omitempty" jsonschema:"if true, return diff without writing the file"`
}

func registerInterfaceTools(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_interface",
		Description: "Add a new interface and its mock atomically. Set mock_file + mock_name to generate a function-field mock with a compile-time var assertion. Prefer this over create for interfaces. preview=true returns a unified diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in interfaceInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		reqDomain := domain.InterfaceActionRequest{
			FilePath: in.File, Identifier: in.Identifier, Content: in.Content,
			MockFile: in.MockFile, MockName: in.MockName,
		}
		var result string
		var addedImports []string
		var diff string
		var writtenFiles []string
		var err error
		if in.Preview {
			diff, writtenFiles, err = runPreview(ctx, commands, func(sc service.SurgeonCommands) error {
				var innerErr error
				result, addedImports, innerErr = sc.AddInterface(ctx, reqDomain)
				return innerErr
			})
		} else {
			result, addedImports, err = commands.AddInterface(ctx, reqDomain)
		}
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (add_interface): %v", err), err), nil, nil
		}
		files := []string{in.File}
		if in.MockFile != "" {
			files = append(files, in.MockFile)
		}
		if in.Preview && len(writtenFiles) > 0 {
			files = writtenFiles
		}
		msg := result
		if in.Preview {
			msg = fmt.Sprintf("PREVIEW (add_interface): %s", result)
			if diff != "" {
				msg += "\n\n" + diff
			}
		}
		res := textResult(msg)
		res.StructuredContent = editOutput{
			FilesModified: files,
			Symbols:       []symbolEdit{{Action: "add_interface", Identifier: in.Identifier, File: in.File}},
			AddedImports:  addedImports,
			Preview:       in.Preview,
			Diff:          diff,
		}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_interface",
		Description: "Replace an interface wholesale and regenerate its mock. For single-method changes, prefer patch_interface. Pass mock_file + mock_name to keep the mock in sync. Doc comments are kept unless you set doc or strip_doc=true. preview=true returns a unified diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in interfaceInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		reqDomain := domain.InterfaceActionRequest{
			FilePath: in.File, Identifier: in.Identifier, Content: in.Content,
			MockFile: in.MockFile, MockName: in.MockName,
			Doc: in.Doc, StripDoc: in.StripDoc,
		}
		var result string
		var addedImports []string
		var diff string
		var writtenFiles []string
		var err error
		if in.Preview {
			diff, writtenFiles, err = runPreview(ctx, commands, func(sc service.SurgeonCommands) error {
				var innerErr error
				result, addedImports, innerErr = sc.UpdateInterface(ctx, reqDomain)
				return innerErr
			})
		} else {
			result, addedImports, err = commands.UpdateInterface(ctx, reqDomain)
		}
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (update_interface): %v", err), err), nil, nil
		}
		files := []string{in.File}
		if in.MockFile != "" {
			files = append(files, in.MockFile)
		}
		if in.Preview && len(writtenFiles) > 0 {
			files = writtenFiles
		}
		msg := result
		if in.Preview {
			msg = fmt.Sprintf("PREVIEW (update_interface): %s", result)
			if diff != "" {
				msg += "\n\n" + diff
			}
		}
		res := textResult(msg)
		res.StructuredContent = editOutput{
			FilesModified: files,
			Symbols:       []symbolEdit{{Action: "update_interface", Identifier: in.Identifier, File: in.File}},
			AddedImports:  addedImports,
			Preview:       in.Preview,
			Diff:          diff,
		}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_interface",
		Description: "Delete an interface. Pass delete_mock=true with mock_file + mock_name to also remove the mock struct, its methods, and the var assertion. The mock file is kept even if empty. preview=true returns a unified diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in interfaceInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		if in.MockFile != "" {
			if err := validateGoFile(in.MockFile); err != nil {
				return err, nil, nil
			}
		}
		reqDomain := domain.InterfaceActionRequest{
			FilePath:   in.File,
			Identifier: in.Identifier,
			MockFile:   in.MockFile,
			MockName:   in.MockName,
			DeleteMock: in.DeleteMock,
		}
		var result string
		var addedImports []string
		var diff string
		var writtenFiles []string
		var err error
		if in.Preview {
			diff, writtenFiles, err = runPreview(ctx, commands, func(sc service.SurgeonCommands) error {
				var innerErr error
				result, addedImports, innerErr = sc.DeleteInterface(ctx, reqDomain)
				return innerErr
			})
		} else {
			result, addedImports, err = commands.DeleteInterface(ctx, reqDomain)
		}
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (delete_interface): %v", err), err), nil, nil
		}
		files := []string{in.File}
		if in.DeleteMock && in.MockFile != "" {
			files = append(files, in.MockFile)
		}
		if in.Preview && len(writtenFiles) > 0 {
			files = writtenFiles
		}
		msg := result
		if in.Preview {
			msg = fmt.Sprintf("PREVIEW (delete_interface): %s", result)
			if diff != "" {
				msg += "\n\n" + diff
			}
		}
		res := textResult(msg)
		res.StructuredContent = editOutput{
			FilesModified: files,
			Symbols:       []symbolEdit{{Action: "delete_interface", Identifier: in.Identifier, File: in.File}},
			AddedImports:  addedImports,
			Preview:       in.Preview,
			Diff:          diff,
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
	Preview  bool   `json:"preview,omitempty" jsonschema:"if true, return diff without writing the file"`
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
		result, err := commands.ExecutePlan(ctx, domain.Plan{
			Preview: in.Preview,
			Actions: []domain.Action{{
				Action:     domain.ActionTypeInsertCall,
				FilePath:   in.File,
				Identifier: in.Function,
				Content:    in.Call,
				Position:   pos,
			}},
		})
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (insert_call): %v", err), err), nil, nil
		}
		var sb strings.Builder
		for _, w := range result.Warnings {
			fmt.Fprintf(&sb, "WARNING: %s\n", w)
		}
		verb := "SUCCESS"
		if result.Preview {
			verb = "PREVIEW"
		}
		fmt.Fprintf(&sb, "%s (insert_call): %d files modified", verb, result.FilesModified)
		if result.Diff != "" {
			sb.WriteString("\n\n")
			sb.WriteString(result.Diff)
		}
		res := textResult(sb.String())
		res.StructuredContent = editOutput{
			FilesModified: result.Files,
			Symbols:       []symbolEdit{{Action: "insert_call", Identifier: in.Function, File: in.File}},
			Warnings:      result.Warnings,
			AddedImports:  result.AddedImports,
			Preview:       result.Preview,
			Diff:          result.Diff,
		}
		return res, nil, nil
	})
}

// --- Other tools ---

type executePlanInput struct {
	Actions []executePlanActionInput `json:"actions" jsonschema:"ordered list of AST actions to execute atomically (up to 15)"`
	Preview bool                     `json:"preview,omitempty" jsonschema:"if true, return diff without writing any files"`
}

type implementInput struct {
	Interface string `json:"interface" jsonschema:"fully qualified interface name, e.g. io.ReadCloser or github.com/org/repo/pkg.Interface"`
	Receiver  string `json:"receiver" jsonschema:"receiver type, e.g. *MyStruct"`
	File      string `json:"file" jsonschema:"target file to append stubs to"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"if true, return diff without writing the file"`
}

type mockInput struct {
	Interface string `json:"interface" jsonschema:"fully qualified interface name"`
	MockName  string `json:"mock_name" jsonschema:"name of the mock struct, e.g. MockBookRepository"`
	File      string `json:"file" jsonschema:"target file to write the mock to"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"if true, return diff without writing the file"`
}

type testInput struct {
	File       string `json:"file" jsonschema:"target Go file containing the function"`
	Identifier string `json:"identifier" jsonschema:"function or method identifier, e.g. NewApp or Book.Validate"`
	Preview    bool   `json:"preview,omitempty" jsonschema:"if true, return diff without writing the test file"`
}

type tagInput struct {
	File       string `json:"file" jsonschema:"target Go file containing the struct"`
	Identifier string `json:"identifier" jsonschema:"struct identifier"`
	Field      string `json:"field,omitempty" jsonschema:"specific field name to update"`
	Set        string `json:"set,omitempty" jsonschema:"exact tag string to set or append"`
	Auto       string `json:"auto,omitempty" jsonschema:"auto-generate tags for exported fields, e.g. json or bson"`
	Preview    bool   `json:"preview,omitempty" jsonschema:"if true, return diff without writing the file"`
}

type extractInterfaceInput struct {
	File       string `json:"file" jsonschema:"target Go file containing the struct"`
	Identifier string `json:"identifier" jsonschema:"struct identifier"`
	Name       string `json:"name" jsonschema:"name of the interface to create"`
	Out        string `json:"out,omitempty" jsonschema:"output file path for the interface"`
	MockFile   string `json:"mock_file,omitempty" jsonschema:"generate mock file path"`
	MockName   string `json:"mock_name,omitempty" jsonschema:"name of the mock struct"`
	Preview    bool   `json:"preview,omitempty" jsonschema:"if true, return diff without writing any file"`
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
	File          string         `json:"file" jsonschema:"target Go file path"`
	Identifier    string         `json:"identifier" jsonschema:"function or method identifier, e.g. FuncName or Receiver.Method"`
	Patches       []patchOpInput `json:"patches" jsonschema:"ordered list of patch operations to apply atomically"`
	Preview       bool           `json:"preview,omitempty" jsonschema:"if true, return diff without writing the file"`
	IncludeNested bool           `json:"include_nested,omitempty" jsonschema:"when true, allow matches inside nested closures (*ast.FuncLit) within the target function. Default: match only the top-level body, excluding closure bodies."`
}

func registerPatchTools(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "patch_function",
		Description: "Edit lines inside one function body by matching on text. Patches are scoped to the named function and applied atomically. " +
			"ops: replace, insert_before, insert_after, delete, wrap, set_signature. match is whitespace-normalized. Disambiguate with occurrence (1-based). " +
			"SIGNATURE EDITS: set_signature rewrites only the params list and/or the results list of the function or method, leaving the body, name, receiver, and any generic type parameters intact. Supply params (with parens) and/or returns; at least one is required. " +
			"LINE TARGETING: use at_line or from_line/to_line with file-absolute line numbers (from symbol body=true) instead of text matching — faster and unambiguous. Mutually exclusive with match/match_regex. " +
			"match_regex: RE2 alternative to match (no backrefs/lookarounds). " +
			"SCOPE: by default, text matches are restricted to the target function's own body and SKIP any nested closure (*ast.FuncLit); pass include_nested=true to search inside closures too. " +
			"NESTED CLOSURES: use identifier 'Parent>closure[N]' (0-based, ordered by AST appearance) to target the Nth *ast.FuncLit inside Parent — subsequent '>closure[M]' segments drill deeper. " +
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
			FilePath:      in.File,
			Identifier:    in.Identifier,
			Patches:       patches,
			Preview:       in.Preview,
			IncludeNested: in.IncludeNested,
		})
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (patch_function): %v", err), err), nil, nil
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
		Name:        "patch_file",
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
			return errorResultWithCode(fmt.Sprintf("ERROR (patch_file): %v", err), err), nil, nil
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
			return errorResultWithCode(fmt.Sprintf("ERROR (patch_struct): %v", err), err), nil, nil
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
			return errorResultWithCode(fmt.Sprintf("ERROR (patch_interface): %v", err), err), nil, nil
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

	mcp.AddTool(s, &mcp.Tool{
		Name:        "implement",
		Description: "Generate method stubs for a struct to satisfy an interface. Already-implemented methods are skipped. Stubs are marked '// TODO(go-surgeon): implement'. Interface must be fully qualified (e.g. io.ReadCloser, github.com/org/repo/pkg.Interface). preview=true returns a unified diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in implementInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		reqDomain := domain.ImplementRequest{
			Interface: in.Interface,
			Receiver:  in.Receiver,
			FilePath:  in.File,
		}
		var results []domain.SymbolResult
		var diff string
		var err error
		if in.Preview {
			diff, _, err = runPreview(ctx, commands, func(sc service.SurgeonCommands) error {
				var innerErr error
				results, innerErr = sc.Implement(ctx, reqDomain)
				return innerErr
			})
		} else {
			results, err = commands.Implement(ctx, reqDomain)
		}
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("failed to implement interface: %v", err), err), nil, nil
		}

		if len(results) == 0 && diff == "" {
			res := textResult("All methods are already implemented.")
			res.StructuredContent = implementOutput{File: in.File, Interface: in.Interface, Receiver: in.Receiver, Stubs: []string{}}
			return res, nil, nil
		}

		var sb strings.Builder
		verb := "Generated"
		if in.Preview {
			verb = "PREVIEW — would generate"
		}
		fmt.Fprintf(&sb, "%s %d missing methods for %s:\n\n", verb, len(results), in.Interface)
		for _, rr := range results {
			fmt.Fprintf(&sb, "Symbol: %s\nReceiver: %s\nFile: %s:%d-%d\nCode:\n%s\n\n",
				rr.Name, rr.Receiver, rr.File, rr.LineStart, rr.LineEnd, rr.Code)
		}
		if diff != "" {
			sb.WriteString(diff)
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
		Description: "Generate a function-field mock for an interface you don't own (stdlib, third-party). For your own interfaces, use add_interface with mock_file instead. Interface must be fully qualified. preview=true returns a unified diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mockInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		reqDomain := domain.MockRequest{
			Interface: in.Interface,
			Receiver:  in.MockName,
			FilePath:  in.File,
		}
		var result, diff string
		var err error
		if in.Preview {
			diff, _, err = runPreview(ctx, commands, func(sc service.SurgeonCommands) error {
				var innerErr error
				result, innerErr = sc.Mock(ctx, reqDomain)
				return innerErr
			})
		} else {
			result, err = commands.Mock(ctx, reqDomain)
		}
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("failed to generate mock: %v", err), err), nil, nil
		}
		msg := result
		if in.Preview {
			msg = "PREVIEW (mock): " + result
			if diff != "" {
				msg += "\n\n" + diff
			}
		}
		res := textResult(msg)
		res.StructuredContent = mockOutput{File: in.File, Interface: in.Interface, MockName: in.MockName}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "test",
		Description: "Scaffold a table-driven _test.go for a function or method. Handles boilerplate (t.Run loop, tt struct, receiver setup). identifier: 'FuncName' or 'Type.Method'. preview=true returns a unified diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in testInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		var testFile, diff string
		var err error
		if in.Preview {
			diff, _, err = runPreview(ctx, commands, func(sc service.SurgeonCommands) error {
				var innerErr error
				testFile, innerErr = sc.GenerateTest(ctx, in.File, in.Identifier)
				return innerErr
			})
		} else {
			testFile, err = commands.GenerateTest(ctx, in.File, in.Identifier)
		}
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("failed to generate test: %v", err), err), nil, nil
		}
		msg := fmt.Sprintf("SUCCESS: Generated test skeleton in %s", testFile)
		if in.Preview {
			msg = fmt.Sprintf("PREVIEW: would generate test skeleton in %s", testFile)
			if diff != "" {
				msg += "\n\n" + diff
			}
		}
		res := textResult(msg)
		res.StructuredContent = testOutput{TestFile: testFile, Identifier: in.Identifier}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "tag",
		Description: "Manage struct field tags. Bulk: auto='json'/'bson' generates snake_case tags on all exported fields. Targeted: field + set updates one field's tag. patch_struct set_tag is an alternative for single fields. preview=true returns a unified diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in tagInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		reqDomain := domain.TagRequest{
			FilePath:   in.File,
			StructName: in.Identifier,
			FieldName:  in.Field,
			SetTag:     in.Set,
			AutoFormat: in.Auto,
		}
		var diff string
		var err error
		if in.Preview {
			diff, _, err = runPreview(ctx, commands, func(sc service.SurgeonCommands) error {
				return sc.TagStruct(ctx, reqDomain)
			})
		} else {
			err = commands.TagStruct(ctx, reqDomain)
		}
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("failed to update tags: %v", err), err), nil, nil
		}
		msg := fmt.Sprintf("SUCCESS: Updated tags for %s in %s", in.Identifier, in.File)
		if in.Preview {
			msg = fmt.Sprintf("PREVIEW (tag): %s in %s", in.Identifier, in.File)
			if diff != "" {
				msg += "\n\n" + diff
			}
		}
		res := textResult(msg)
		res.StructuredContent = tagOutput{File: in.File, Identifier: in.Identifier, Field: in.Field}
		return res, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "extract_interface",
		Description: "Derive an interface from a struct's exported methods. Use out to place it in a specific file. Set mock_file + mock_name to generate the mock in the same step. preview=true returns a unified diff without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in extractInterfaceInput) (*mcp.CallToolResult, any, error) {
		if err := validateGoFile(in.File); err != nil {
			return err, nil, nil
		}
		reqDomain := domain.ExtractInterfaceRequest{
			FilePath:      in.File,
			StructName:    in.Identifier,
			InterfaceName: in.Name,
			OutPath:       in.Out,
			MockFile:      in.MockFile,
			MockName:      in.MockName,
		}
		var interfaceFile, diff string
		var err error
		if in.Preview {
			diff, _, err = runPreview(ctx, commands, func(sc service.SurgeonCommands) error {
				var innerErr error
				interfaceFile, innerErr = sc.ExtractInterface(ctx, reqDomain)
				return innerErr
			})
		} else {
			interfaceFile, err = commands.ExtractInterface(ctx, reqDomain)
		}
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("failed to extract interface: %v", err), err), nil, nil
		}
		msg := fmt.Sprintf("SUCCESS: Extracted interface %s into %s", in.Name, interfaceFile)
		if in.Preview {
			msg = fmt.Sprintf("PREVIEW: would extract interface %s into %s", in.Name, interfaceFile)
			if diff != "" {
				msg += "\n\n" + diff
			}
		}
		res := textResult(msg)
		res.StructuredContent = extractInterfaceOutput{InterfaceName: in.Name, InterfaceFile: interfaceFile, MockFile: in.MockFile, MockName: in.MockName}
		return res, nil, nil
	})
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

type testRunInput struct {
	Dir            string `json:"dir,omitempty" jsonschema:"directory to test (relative to the project root). Defaults to ./..."`
	Run            string `json:"run,omitempty" jsonschema:"optional -run regexp filter"`
	Count          int    `json:"count,omitempty" jsonschema:"iterations per test (default 1)"`
	Race           bool   `json:"race,omitempty" jsonschema:"enable the race detector"`
	Tags           string `json:"tags,omitempty" jsonschema:"build tags (whitelist [a-z_][a-z0-9_,.]*)"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"overall timeout in seconds (default 120, max 600)"`
	AffectedBy     string `json:"affected_by,omitempty" jsonschema:"path to a .go file — narrow the test run to the package that owns this file plus every package in the module that (transitively) imports it. Mutually exclusive with dir. Great after editing one file in a large monorepo — skips running tests in unrelated packages."`
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
			return errorResultWithCode(fmt.Sprintf("ERROR (patch_decl): %v", err), err), nil, nil
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
