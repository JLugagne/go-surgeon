package mcp

import (
	"context"
	"fmt"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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
	File        string `json:"file,omitempty" jsonschema:"absolute or relative path to the Go file; required when at_line is set"`
	AtLine      int    `json:"at_line,omitempty" jsonschema:"resolve the outermost declaration that spans this 1-based line number in file; mutually exclusive with query and pattern"`
	MaxResults  int    `json:"max_results,omitempty" jsonschema:"pattern mode only: cap the number of results returned; 0 = unlimited (default). Use to avoid large payloads with broad patterns."`
}

type testRunInput struct {
	Dir              string   `json:"dir,omitempty" jsonschema:"directory to test (relative to the project root). Defaults to ./..."`
	Run              string   `json:"run,omitempty" jsonschema:"optional -run regexp filter"`
	Count            int      `json:"count,omitempty" jsonschema:"iterations per test (default 1)"`
	Race             bool     `json:"race,omitempty" jsonschema:"enable the race detector"`
	Tags             string   `json:"tags,omitempty" jsonschema:"build tags (whitelist [a-z_][a-z0-9_,.]*)"`
	TimeoutSeconds   int      `json:"timeout_seconds,omitempty" jsonschema:"overall timeout in seconds (default 120, max 600)"`
	AffectedBy       string   `json:"affected_by,omitempty" jsonschema:"path to a .go file — narrow the test run to the package that owns this file plus every package in the module that (transitively) imports it. Mutually exclusive with dir and symbols."`
	Symbols          []string `json:"symbols,omitempty" jsonschema:"list of symbols in the form 'FuncName' or 'pkg.FuncName'. Each symbol is resolved to its owning package and a -run filter is built from Go naming conventions (^TestFuncName). Mutually exclusive with dir and affected_by. Use when you want to run only tests related to specific functions."`
	Verbosity        string   `json:"verbosity,omitempty" jsonschema:"output verbosity: 'summary' returns only success, summary, and failed tests (compact ~1k chars regardless of suite size — ideal for large suites); 'full' returns everything including raw_output and per-test elapsed_ms. Defaults to auto: summary when the suite has more than 50 tests, full otherwise. Pass 'full' to force the verbose payload, 'summary' to force the compact one."`
	IncludeRawOutput bool     `json:"include_raw_output,omitempty" jsonschema:"include the verbatim go test -json stream in structured output. By default RawOutput is dropped from the structured payload on success (where it bloats responses without adding signal); failures always keep it. Set true to force inclusion (e.g. when you want full test output regardless of pass/fail)."`
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
		Description: "Read one declaration (exact query='Name'/'Receiver.Method'/'pkg.Name') or list matches (pattern=regex). To resolve a file:line diagnostic directly, set file+at_line — returns the outermost named declaration that spans that line, no name lookup needed. body=true shows the implementation plus the file's package line and import block. In pattern mode, outline=true returns signature + first-sentence doc summary per match (middle ground between signature-only and body=true). When exploring an unfamiliar file, use context=file to also get an outline of every sibling declaration — saves 5+ follow-up calls. Works on dependencies via module='github.com/org/repo'. query, pattern, and at_line are mutually exclusive.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in symbolInput) (*mcp.CallToolResult, any, error) {
		dir := in.Dir
		if dir == "" {
			dir = "."
		}

		atLineSet := in.AtLine > 0
		if atLineSet && (in.Query != "" || in.Pattern != "") {
			return errorResult("at_line is mutually exclusive with query and pattern"), nil, nil
		}
		if in.Query != "" && in.Pattern != "" {
			return errorResult("query and pattern are mutually exclusive — set one"), nil, nil
		}
		if !atLineSet && in.Query == "" && in.Pattern == "" {
			return errorResult("one of query, pattern, or at_line (with file) is required"), nil, nil
		}
		if atLineSet && in.File == "" {
			return errorResult("file is required when at_line is set"), nil, nil
		}

		if atLineSet {
			results, err := queries.FindSymbols(ctx, domain.SymbolQuery{File: in.File, AtLine: in.AtLine, Context: in.Context, Tests: in.Tests}, dir)
			if err != nil {
				return errorResultWithCode(err.Error(), err), nil, nil
			}
			if len(results) == 0 {
				hint := fmt.Sprintf("Line %d is not a named declaration (it may be a package clause, import block, blank line, or comment). Try a line inside a func/type/const/var body, or use query='SymbolName' instead.", in.AtLine)
				return textResult(fmt.Sprintf("No declaration found at %s:%d.\n%s", in.File, in.AtLine, hint)), nil, nil
			}
			text := formatSymbolResults(results, in.Body, fmt.Sprintf("%s:%d", in.File, in.AtLine))
			return textResult(text), nil, nil
		}

		if in.Pattern != "" {
			results, err := queries.FindSymbols(ctx, domain.SymbolQuery{Pattern: in.Pattern, Tests: in.Tests, Module: in.Module, Context: in.Context, MaxResults: in.MaxResults}, dir)
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
		Description: "Run `go test` scoped to a package/directory and return a compact pass/fail report with per-test timing and failure file:line references. Use after editing Go code to verify behavior in-loop. dir defaults to ./..., timeout defaults to 120s (max 600). Pass affected_by=path/to/file.go to run only the owning package plus its reverse-dependency closure (mutually exclusive with dir and symbols). Pass symbols=['pkg.MyFunc'] to auto-resolve the owning package and build a -run filter matching ^TestMyFunc — ideal when you only want tests related to specific functions you just edited; mutually exclusive with dir and affected_by. On success the verbatim raw_output stream is omitted from the structured payload (it bloats responses without adding signal); set include_raw_output=true to force it on a green run. verbosity controls payload size on large suites: 'summary' returns only success/summary/failed tests (~1k chars regardless of suite size — ideal for the 25k token tool-result budget); 'full' keeps everything; default auto picks 'summary' once the suite has more than 50 tests.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in testRunInput) (*mcp.CallToolResult, any, error) {
		result, err := queries.TestRun(ctx, domain.TestRunRequest{
			Dir:            in.Dir,
			Run:            in.Run,
			Count:          in.Count,
			Race:           in.Race,
			Tags:           in.Tags,
			TimeoutSeconds: in.TimeoutSeconds,
			AffectedBy:     in.AffectedBy,
			Symbols:        in.Symbols,
		})
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("ERROR (test_run): %v", err), err), nil, nil
		}
		if result.Success && !in.IncludeRawOutput {
			result.RawOutput = ""
		}
		structured := applyTestRunVerbosity(result, in.Verbosity, in.IncludeRawOutput)
		res := textResult(formatTestRunResult(result))
		res.StructuredContent = structured
		return res, nil, nil
	})
}
