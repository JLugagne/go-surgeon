package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// describeToolInput drives the describe_tool MCP tool. All fields are
// optional: with no args we emit a grouped list of every tool; with
// name we emit the single tool's detail; with category we filter to
// one group.
type describeToolInput struct {
	Name     string `json:"name,omitempty" jsonschema:"describe a single tool by name (mutually exclusive with category)"`
	Category string `json:"category,omitempty" jsonschema:"filter to one category: explore, edit, validate, interface, codegen, refs, batch, meta"`
}

// toolEntry is one row in the tool catalog. Keeping this data
// alongside the actual tool registrations (in a separate file) means
// the catalog can drift — the test at the bottom of this file asserts
// every registered tool has an entry, which catches drift fast.
type toolEntry struct {
	Name     string
	Category string // explore | edit | validate | interface | codegen | refs | batch | meta
	Summary  string // one-line "when to use"
	Example  string // optional canonical example JSON arguments
	Related  string // comma-separated names of related tools (optional)
}

// toolCatalog is the machine-readable version of serverInstructions.
// Keep entries sorted by (category, name) for deterministic output.
var toolCatalog = []toolEntry{
	// EXPLORE
	{Name: "overview", Category: "explore", Summary: "list packages + symbols across a project; START HERE on unfamiliar codebases", Example: `{"dir": "internal", "symbols": true}`, Related: "symbol"},
	{Name: "symbol", Category: "explore", Summary: "read one declaration (query='Name'/'Receiver.Method') or list matches by regex (pattern='...')", Example: `{"query": "NewServer", "body": true}`, Related: "overview, find_definition"},

	// REFS (symbol-level cross-reference + rename)
	{Name: "find_definition", Category: "refs", Summary: "type-aware: locate a symbol's declaration across packages", Example: `{"name": "NewServer"}`, Related: "symbol, find_references"},
	{Name: "find_references", Category: "refs", Summary: "type-aware: list every use of a symbol, cross-package, deduplicated", Example: `{"name": "NewServer", "include_definition": true}`, Related: "find_definition, rename_symbol"},
	{Name: "rename_symbol", Category: "refs", Summary: "type-aware: rename a symbol and every reference; blocks export-status flips and in-scope collisions; preview=true for dry run", Example: `{"name": "OldName", "new_name": "NewName", "preview": true}`, Related: "find_references"},

	// EDIT — narrowest first
	{Name: "patch_function", Category: "edit", Summary: "edit lines inside one function body (literal/regex match, at_line, set_signature, insert_*)", Example: `{"file": "foo.go", "identifier": "Foo", "patches": [{"op": "replace", "match": "x", "replace": "y"}]}`, Related: "patch_file, update"},
	{Name: "patch_struct", Category: "edit", Summary: "edit a struct's field list (add/remove/rename/retype/set_tag/set_doc)", Example: `{"file": "foo.go", "identifier": "User", "patches": [{"op": "add_field", "name": "ID", "type": "string"}]}`, Related: "patch_interface, tag"},
	{Name: "patch_interface", Category: "edit", Summary: "edit an interface's method set + regenerate its mock; atomic ops (add/remove/rename/retype/set_doc/embed)", Example: `{"file": "foo.go", "identifier": "Reader", "patches": [{"op": "add_method", "signature": "Close() error"}]}`, Related: "patch_struct, update_interface"},
	{Name: "patch_file", Category: "edit", Summary: "whole-file text substitution with AST safety; cross-function batch edits only", Example: `{"file": "foo.go", "patches": [{"match": "oldName", "replace": "newName"}]}`, Related: "patch_function, rename_symbol"},
	{Name: "patch_decl", Category: "edit", Summary: "edit the value of a top-level const or var (multi-line strings, error vars, …)", Example: `{"file": "foo.go", "identifier": "banner", "patches": [{"op": "replace", "match": "v1", "replace": "v2"}]}`, Related: "patch_function"},
	{Name: "insert_call", Category: "edit", Summary: "insert one statement at a marked position inside a function body (before-return / end-of-body / after-marker)", Example: `{"file": "foo.go", "identifier": "Handler", "content": "log.Println(\"hi\")", "position": "end-of-body"}`, Related: "patch_function"},
	{Name: "create", Category: "edit", Summary: "add a new file, function, or struct (object=file|func|struct)", Example: `{"object": "func", "file": "foo.go", "content": "func Foo() {}"}`, Related: "update, execute_plan"},
	{Name: "update", Category: "edit", Summary: "whole-declaration replacement (replace_file / update_func / update_struct); prefer patch_* when editing in place", Example: `{"object": "func", "file": "foo.go", "identifier": "Foo", "content": "func Foo() {}"}`, Related: "patch_function, create"},
	{Name: "delete", Category: "edit", Summary: "remove a function, method, or struct (object=func|struct)", Example: `{"object": "func", "file": "foo.go", "identifier": "Foo"}`, Related: "delete_interface"},

	// INTERFACE (composite ops: interface + mock in lockstep)
	{Name: "add_interface", Category: "interface", Summary: "create an interface AND its mock atomically", Example: `{"file": "foo.go", "identifier": "Reader", "content": "Read(p []byte) (int, error)", "mock_file": "mock_reader.go", "mock_name": "MockReader"}`, Related: "patch_interface, mock"},
	{Name: "update_interface", Category: "interface", Summary: "replace an interface's full declaration AND keep its mock in sync", Example: `{"file": "foo.go", "identifier": "Reader", "content": "...", "mock_file": "mock_reader.go"}`, Related: "patch_interface"},
	{Name: "delete_interface", Category: "interface", Summary: "remove an interface and (optionally) its mock", Example: `{"file": "foo.go", "identifier": "Reader", "delete_mock": true}`, Related: "delete"},

	// CODEGEN
	{Name: "implement", Category: "codegen", Summary: "generate method stubs on a struct for an interface it doesn't yet satisfy", Example: `{"file": "foo.go", "receiver": "*Foo", "interface": "io.Reader"}`, Related: "add_interface"},
	{Name: "mock", Category: "codegen", Summary: "generate a standalone mock for an interface you don't own (stdlib/third-party)", Example: `{"file": "mock.go", "identifier": "io.Reader"}`, Related: "add_interface"},
	{Name: "extract_interface", Category: "codegen", Summary: "derive an interface from an existing struct's exported methods", Example: `{"file": "foo.go", "struct": "FooService", "interface": "FooAPI"}`, Related: "add_interface"},
	{Name: "test", Category: "codegen", Summary: "generate a table-driven test skeleton for a function or method", Example: `{"file": "foo.go", "identifier": "Foo"}`, Related: "test_run"},
	{Name: "tag", Category: "codegen", Summary: "bulk-generate or set struct field tags (json, bson, …)", Example: `{"file": "foo.go", "identifier": "User", "tag": "json"}`, Related: "patch_struct"},

	// VALIDATE
	{Name: "build_check", Category: "validate", Summary: "run go build and return structured compile diagnostics; affected_by=file narrows to that file's reverse-dep closure", Example: `{"affected_by": "internal/foo/bar.go"}`, Related: "test_run"},
	{Name: "test_run", Category: "validate", Summary: "run go test and return a compact pass/fail report; affected_by=file narrows to owning package + reverse-deps", Example: `{"affected_by": "internal/foo/bar.go"}`, Related: "build_check, test"},

	// BATCH
	{Name: "execute_plan", Category: "batch", Summary: "apply up to 15 edit actions atomically (create/update/delete/patch_*); use when 3+ edits must land together or roll back together", Example: `{"actions": [{"action": "patch_function", "file": "a.go", "identifier": "Foo", "patch_function_ops": [...]}]}`, Related: "patch_function, create"},
	{Name: "batch_query", Category: "batch", Summary: "run up to 10 read-only queries (symbol/overview/find_references/find_definition) in one round-trip; fail-soft per item", Example: `{"queries": [{"op": "overview", "focus": "internal"}, {"op": "symbol", "query": "NewServer"}]}`, Related: "symbol, overview"},

	// META
	{Name: "describe_tool", Category: "meta", Summary: "query this catalog: no args → grouped list; name=X → detail; category=X → filtered list", Example: `{"name": "patch_function"}`},
}

// categoryOrder controls the order groups appear in the grouped list.
// Order is intentional: exploration → narrow edits → broad edits →
// composites → codegen → validate → batch → meta.
var categoryOrder = []string{"explore", "refs", "edit", "interface", "codegen", "validate", "batch", "meta"}

// categoryLabels maps catalog codes to the prose labels agents see.
var categoryLabels = map[string]string{
	"explore":   "EXPLORE",
	"refs":      "REFS & RENAME",
	"edit":      "EDIT",
	"interface": "INTERFACE (interface + mock lockstep)",
	"codegen":   "CODE GENERATION",
	"validate":  "VALIDATE",
	"batch":     "BATCH",
	"meta":      "META",
}

func registerDescribeTool(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "describe_tool",
		Description: "Queryable catalog of every go-surgeon tool. No args returns a grouped 'when to use' list; name=X returns one tool's detail (summary, example, related tools); category=X filters to one group (explore, refs, edit, interface, codegen, validate, batch, meta).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in describeToolInput) (*mcp.CallToolResult, any, error) {
		if in.Name != "" && in.Category != "" {
			return errorResult("describe_tool: name and category are mutually exclusive"), nil, nil
		}
		if in.Name != "" {
			return renderDescribeOne(in.Name), nil, nil
		}
		return renderDescribeList(in.Category), nil, nil
	})
}

// renderDescribeOne returns the single-tool detail view.
func renderDescribeOne(name string) *mcp.CallToolResult {
	for _, e := range toolCatalog {
		if e.Name == name {
			var sb strings.Builder
			fmt.Fprintf(&sb, "%s (%s)\n", e.Name, e.Category)
			fmt.Fprintf(&sb, "  %s\n", e.Summary)
			if e.Example != "" {
				fmt.Fprintf(&sb, "  example: %s\n", e.Example)
			}
			if e.Related != "" {
				fmt.Fprintf(&sb, "  see also: %s\n", e.Related)
			}
			return textResult(sb.String())
		}
	}
	return errorResult(fmt.Sprintf("describe_tool: unknown tool %q (try describe_tool with no args to see the catalog)", name))
}

// renderDescribeList returns the grouped list view; when category is
// set, only entries from that group are emitted.
func renderDescribeList(category string) *mcp.CallToolResult {
	if category != "" {
		if _, ok := categoryLabels[category]; !ok {
			return errorResult(fmt.Sprintf("describe_tool: unknown category %q (valid: %s)", category, strings.Join(categoryOrder, ", ")))
		}
	}
	grouped := map[string][]toolEntry{}
	for _, e := range toolCatalog {
		grouped[e.Category] = append(grouped[e.Category], e)
	}
	for k := range grouped {
		rows := grouped[k]
		sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
		grouped[k] = rows
	}
	var sb strings.Builder
	for _, cat := range categoryOrder {
		if category != "" && cat != category {
			continue
		}
		rows := grouped[cat]
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "%s\n", categoryLabels[cat])
		for _, r := range rows {
			fmt.Fprintf(&sb, "  %-20s %s\n", r.Name, r.Summary)
		}
		sb.WriteByte('\n')
	}
	if sb.Len() == 0 {
		return textResult("(no tools matched)")
	}
	return textResult(strings.TrimRight(sb.String(), "\n") + "\n")
}
