package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxBatchQueries caps the number of sub-queries per batch_query call.
// Matches the typical agent ceiling before a batch starts exceeding the
// per-turn output budget.
const maxBatchQueries = 10

// batchQueryInput is the top-level input for the batch_query MCP tool.
// It accepts up to maxBatchQueries read-only sub-queries and returns
// one result per item in the same order.
type batchQueryInput struct {
	Queries []batchQueryItem `json:"queries" jsonschema:"list of read-only queries to run; each result is returned in the same order (max 10)"`
}

// batchQueryItem is one sub-query within a batch_query call.
// Op selects the operation; the remaining fields are the union of
// inputs accepted by the underlying per-tool schemas. Unused fields
// for a given op are ignored.
type batchQueryItem struct {
	Op string `json:"op" jsonschema:"one of: symbol, overview, find_references, find_definition"`

	// Shared fields.
	Dir    string `json:"dir,omitempty" jsonschema:"directory to operate in (defaults to '.')"`
	Module string `json:"module,omitempty" jsonschema:"import path of a dependency to explore instead of the current project"`
	Tests  bool   `json:"tests,omitempty" jsonschema:"include _test.go files"`

	// symbol fields.
	Query   string `json:"query,omitempty" jsonschema:"(symbol) exact symbol name: Name, Receiver.Method, or pkg.Name"`
	Pattern string `json:"pattern,omitempty" jsonschema:"(symbol) regex to match against declaration names"`
	Body    bool   `json:"body,omitempty" jsonschema:"(symbol) show the full function or struct body"`
	Context string `json:"context,omitempty" jsonschema:"(symbol) 'file' to also return the file outline"`

	// overview fields.
	Symbols   bool     `json:"symbols,omitempty" jsonschema:"(overview) include exported symbols per file"`
	Summary   bool     `json:"summary,omitempty" jsonschema:"(overview) append package doc comment summary"`
	Deps      bool     `json:"deps,omitempty" jsonschema:"(overview) show internal package import dependencies"`
	Recursive bool     `json:"recursive,omitempty" jsonschema:"(overview) walk sub-packages when symbols is set"`
	Focus     string   `json:"focus,omitempty" jsonschema:"(overview) package path for full detail"`
	Exclude   []string `json:"exclude,omitempty" jsonschema:"(overview) glob patterns for directories to skip"`

	// find_references / find_definition fields.
	Name              string `json:"name,omitempty" jsonschema:"(find_*) symbol name"`
	Receiver          string `json:"receiver,omitempty" jsonschema:"(find_*) receiver type name for methods"`
	Package           string `json:"package,omitempty" jsonschema:"(find_*) package import path or name"`
	File              string `json:"file,omitempty" jsonschema:"(find_*) file path to pin an exact declaration"`
	Line              int    `json:"line,omitempty" jsonschema:"(find_*) 1-based declaration line"`
	IncludeDefinition bool   `json:"include_definition,omitempty" jsonschema:"(find_references) also return the definition site"`
}

// batchQueryResult is one entry in the batch_query structured output.
// Exactly one of Error or Result is set. Index mirrors the position
// of the corresponding input item.
type batchQueryResult struct {
	Index  int    `json:"index"`
	Op     string `json:"op"`
	Error  string `json:"error,omitempty"`
	Result any    `json:"result,omitempty"`
}

// batchQueryOutput is the structured content returned by batch_query.
type batchQueryOutput struct {
	Results []batchQueryResult `json:"results"`
}

// registerBatchQueryTool wires the batch_query MCP tool. It dispatches
// each sub-query to the same underlying service methods used by the
// individual tools, sharing the packages loader cache so N queries are
// dramatically cheaper than N separate MCP calls.
func registerBatchQueryTool(s *mcp.Server, queries service.SurgeonQueries) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "batch_query",
		Description: "Run up to 10 read-only queries (symbol, overview, find_references, find_definition) in one round-trip. Results are returned in input order. Fail-soft: a failing sub-query is reported as an error entry and the others still return. Share the packages loader cache across items — cheaper than N separate calls on a single repo.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in batchQueryInput) (*mcp.CallToolResult, any, error) {
		if len(in.Queries) == 0 {
			return errorResult("batch_query: queries is required and must contain at least one item"), nil, nil
		}
		if len(in.Queries) > maxBatchQueries {
			return errorResult(fmt.Sprintf("batch_query: too many sub-queries (%d); maximum is %d", len(in.Queries), maxBatchQueries)), nil, nil
		}

		results := make([]batchQueryResult, len(in.Queries))
		var sb strings.Builder

		for i, item := range in.Queries {
			entry := batchQueryResult{Index: i, Op: item.Op}
			text, structured, err := dispatchBatchItem(ctx, queries, item)

			fmt.Fprintf(&sb, "=== [%d] %s ===\n", i, item.Op)
			if err != nil {
				entry.Error = err.Error()
				fmt.Fprintf(&sb, "ERROR: %s\n", err.Error())
			} else {
				entry.Result = structured
				sb.WriteString(text)
				if !strings.HasSuffix(text, "\n") {
					sb.WriteByte('\n')
				}
			}
			sb.WriteByte('\n')
			results[i] = entry
		}

		out := textResult(strings.TrimRight(sb.String(), "\n"))
		out.StructuredContent = batchQueryOutput{Results: results}
		return out, nil, nil
	})
}

// dispatchBatchItem runs one sub-query and returns the formatted text,
// the structured payload, and any error. Errors are returned rather
// than wrapped into an errorResult so the caller can mark the entry as
// failed while continuing with the remaining items.
func dispatchBatchItem(ctx context.Context, queries service.SurgeonQueries, item batchQueryItem) (string, any, error) {
	switch item.Op {
	case "symbol":
		return dispatchBatchSymbol(ctx, queries, item)
	case "overview":
		return dispatchBatchOverview(ctx, queries, item)
	case "find_references":
		return dispatchBatchFindReferences(ctx, queries, item)
	case "find_definition":
		return dispatchBatchFindDefinition(ctx, queries, item)
	case "":
		return "", nil, fmt.Errorf("op is required (one of: symbol, overview, find_references, find_definition)")
	default:
		return "", nil, fmt.Errorf("unknown op %q (expected one of: symbol, overview, find_references, find_definition)", item.Op)
	}
}

func dispatchBatchSymbol(ctx context.Context, queries service.SurgeonQueries, item batchQueryItem) (string, any, error) {
	dir := item.Dir
	if dir == "" {
		dir = "."
	}
	if item.Query != "" && item.Pattern != "" {
		return "", nil, fmt.Errorf("symbol: query and pattern are mutually exclusive")
	}
	if item.Query == "" && item.Pattern == "" {
		return "", nil, fmt.Errorf("symbol: one of query or pattern is required")
	}

	if item.Pattern != "" {
		results, err := queries.FindSymbols(ctx, domain.SymbolQuery{
			Pattern: item.Pattern,
			Tests:   item.Tests,
			Module:  item.Module,
			Context: item.Context,
		}, dir)
		if err != nil {
			return "", nil, err
		}
		if len(results) == 0 {
			return fmt.Sprintf("No declarations match pattern %q.", item.Pattern), results, nil
		}
		text := formatPatternResults(results, item.Body, item.Pattern, 0, false)
		return text, results, nil
	}

	results := findSymbols(ctx, queries, item.Query, item.Tests, dir, item.Module, item.Context)
	if len(results) == 0 {
		return fmt.Sprintf("No matches found for '%s'.", item.Query), results, nil
	}
	text := formatSymbolResults(results, item.Body, item.Query)
	return text, results, nil
}

func dispatchBatchOverview(ctx context.Context, queries service.SurgeonQueries, item batchQueryItem) (string, any, error) {
	dir := item.Dir
	if dir == "" {
		dir = "."
	}
	opts := domain.GraphOptions{
		Dir:       dir,
		Symbols:   item.Symbols,
		Summary:   item.Summary,
		Deps:      item.Deps,
		Recursive: item.Recursive,
		Tests:     item.Tests,
		Focus:     item.Focus,
		Exclude:   item.Exclude,
		Module:    item.Module,
	}
	if opts.Focus != "" {
		opts.Symbols = true
		opts.Summary = true
		opts.Recursive = true
	}
	packages, err := queries.Graph(ctx, opts)
	if err != nil {
		return "", nil, err
	}
	text := formatGraph(packages, opts)
	return text, packages, nil
}

func dispatchBatchFindReferences(ctx context.Context, queries service.SurgeonQueries, item batchQueryItem) (string, any, error) {
	if item.Name == "" {
		return "", nil, fmt.Errorf("find_references: name is required")
	}
	q := domain.ReferencesQuery{
		Symbol: domain.SymbolRef{
			Name:     item.Name,
			Receiver: item.Receiver,
			Package:  item.Package,
			File:     item.File,
			Line:     item.Line,
		},
		Dir:               item.Dir,
		Tests:             item.Tests,
		IncludeDefinition: item.IncludeDefinition,
		Module:            item.Module,
	}
	result, err := queries.FindReferences(ctx, q)
	if err != nil {
		return "", nil, err
	}
	text := formatReferences(result, item.IncludeDefinition)
	return text, referencesStructured(result, item.IncludeDefinition), nil
}

func dispatchBatchFindDefinition(ctx context.Context, queries service.SurgeonQueries, item batchQueryItem) (string, any, error) {
	if item.Name == "" {
		return "", nil, fmt.Errorf("find_definition: name is required")
	}
	q := domain.ReferencesQuery{
		Symbol: domain.SymbolRef{
			Name:     item.Name,
			Receiver: item.Receiver,
			Package:  item.Package,
			File:     item.File,
			Line:     item.Line,
		},
		Dir:    item.Dir,
		Tests:  item.Tests,
		Module: item.Module,
	}
	result, err := queries.FindDefinition(ctx, q)
	if err != nil {
		return "", nil, err
	}
	text := formatDefinition(result)
	return text, definitionStructured(result), nil
}
