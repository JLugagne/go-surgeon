package mcp

import (
	"context"
	"encoding/json"
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
	Format   string `json:"format,omitempty" jsonschema:"output format: 'text' (default) or 'json'"`
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
	// optional list of known limitations / edge cases with workarounds — surfaced under a 'limitations:' section in the per-tool detail view
	Limitations []string
	Ops         map[string]toolOpEntry
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
	{Name: "patch", Category: "edit", Summary: "edit Go source by target: function (body lines), struct (fields), interface (methods+mock), file (whole-file substitution), decl (const/var values). Always takes items: [{file, identifier, patches, ...}] — length 1 for single-target, length N for batch (function/struct items are atomic across the batch; interface/file/decl items are applied sequentially).", Example: `{"target": "function", "items": [{"file": "foo.go", "identifier": "Foo", "patches": [{"op": "replace", "match": "x", "replace": "y"}]}]}`, Related: "update, execute_plan", Limitations: []string{
		"multi-line replacement: op=replace can mis-splice multi-line replacements (issues #3, #14) — patch validates results post-splice and refuses with PATCH_REPLACE_NOT_APPLIED or PATCH_DROPPED_CONTENT when content is dropped, leaving the file unchanged. Workaround: use 'update object=func' (or update object=file/struct) with the full new declaration",
		"replacement containing tabs/escapes: literal tab characters and escape sequences inside a multi-line replace value can confuse the splice — workaround: use 'update object=func' which takes raw Go source verbatim",
		"large struct-literal field insertion: inserting many fields into a big struct literal via op=replace is fragile — workaround: use 'update object=func' to rewrite the whole declaration that contains the literal",
	}, Ops: patchOps},
	{Name: "patch.function", Category: "edit", Summary: "ops for target=function: replace, insert_before, insert_after, delete, wrap, set_signature", Ops: patchFunctionOps},
	{Name: "patch.struct", Category: "edit", Summary: "ops for target=struct: add_field, remove_field, rename_field, retype_field, set_tag, set_doc", Ops: patchStructOps},
	{Name: "patch.interface", Category: "edit", Summary: "ops for target=interface: add_method, remove_method, rename_method, retype_method, set_doc, embed, remove_embed", Ops: patchInterfaceOps},
	{Name: "patch.decl", Category: "edit", Summary: "ops for target=decl: replace, insert_before, insert_after, delete, wrap", Ops: patchDeclOps},
	{Name: "insert_call", Category: "edit", Summary: "insert one statement at a marked position inside a function body (before-return / end-of-body / after-marker)", Example: `{"file": "foo.go", "identifier": "Handler", "content": "log.Println(\"hi\")", "position": "end-of-body"}`, Related: "patch"},
	{Name: "create", Category: "edit", Summary: "add a new file, function, or struct (object=file|func|struct)", Example: `{"object": "func", "file": "foo.go", "content": "func Foo() {}"}`, Related: "update, execute_plan"},
	{Name: "update", Category: "edit", Summary: "whole-declaration replacement (replace_file / update_func / update_struct); prefer patch when editing in place", Example: `{"object": "func", "file": "foo.go", "identifier": "Foo", "content": "func Foo() {}"}`, Related: "patch, create"},
	{Name: "delete", Category: "edit", Summary: "remove a function, method, or struct (object=func|struct)", Example: `{"object": "func", "file": "foo.go", "identifier": "Foo"}`, Related: "delete_interface"},

	// INTERFACE (composite ops: interface + mock in lockstep)
	{Name: "add_interface", Category: "interface", Summary: "create an interface AND its mock atomically", Example: `{"file": "foo.go", "identifier": "Reader", "content": "Read(p []byte) (int, error)", "mock_file": "mock_reader.go", "mock_name": "MockReader"}`, Related: "patch, mock"},
	{Name: "update_interface", Category: "interface", Summary: "replace an interface's full declaration AND keep its mock in sync", Example: `{"file": "foo.go", "identifier": "Reader", "content": "...", "mock_file": "mock_reader.go"}`, Related: "patch"},
	{Name: "delete_interface", Category: "interface", Summary: "remove an interface and (optionally) its mock", Example: `{"file": "foo.go", "identifier": "Reader", "delete_mock": true}`, Related: "delete"},

	// CODEGEN
	{Name: "implement", Category: "codegen", Summary: "generate method stubs on a struct for an interface it doesn't yet satisfy", Example: `{"file": "foo.go", "receiver": "*Foo", "interface": "io.Reader"}`, Related: "add_interface"},
	{Name: "mock", Category: "codegen", Summary: "generate a standalone mock for an interface you don't own (stdlib/third-party)", Example: `{"file": "mock.go", "identifier": "io.Reader"}`, Related: "add_interface"},
	{Name: "extract_interface", Category: "codegen", Summary: "derive an interface from an existing struct's exported methods", Example: `{"file": "foo.go", "struct": "FooService", "interface": "FooAPI"}`, Related: "add_interface"},
	{Name: "test", Category: "codegen", Summary: "generate a table-driven test skeleton for a function or method", Example: `{"file": "foo.go", "identifier": "Foo"}`, Related: "test_run"},
	{Name: "tag", Category: "codegen", Summary: "bulk-generate or set struct field tags (json, bson, …)", Example: `{"file": "foo.go", "identifier": "User", "tag": "json"}`, Related: "patch"},

	// VALIDATE
	{Name: "build_check", Category: "validate", Summary: "run go build and return structured compile diagnostics; affected_by=file narrows to that file's reverse-dep closure", Example: `{"affected_by": "internal/foo/bar.go"}`, Related: "test_run"},
	{Name: "test_run", Category: "validate", Summary: "run go test and return a compact pass/fail report; affected_by=file narrows to owning package + reverse-deps; verbosity=summary|full controls payload size (auto-summary above 50 tests)", Example: `{"affected_by": "internal/foo/bar.go", "verbosity": "summary"}`, Related: "build_check, test"},

	// BATCH
	{Name: "execute_plan", Category: "batch", Summary: "apply up to 15 edit actions atomically (create/update/delete/patch_*); use when 3+ edits must land together or roll back together", Example: `{"actions": [{"action": "patch", "file": "a.go", "identifier": "Foo", "target": "function", "patch_function_ops": [...]}]}`, Related: "patch, create"},
	{Name: "batch_query", Category: "batch", Summary: "run up to 10 read-only queries (symbol/overview/find_references/find_definition) in one round-trip; fail-soft per item", Example: `{"queries": [{"op": "overview", "focus": "internal"}, {"op": "symbol", "query": "NewServer"}]}`, Related: "symbol, overview"},
	// META
	{Name: "describe_tool", Category: "meta", Summary: "query this catalog: no args → grouped list; name=X → detail; category=X → filtered list", Example: `{"name": "patch"}`},
}

// patchOps, patchFunctionOps, patchStructOps, patchInterfaceOps, and
// patchDeclOps describe the per-op help emitted by
// `describe_tool name=tool.op`. Keep descriptions to 1-2 sentences
// and the example minimal.

// patchOps is the top-level ops map for the unified patch tool; it
// documents the target discriminator values rather than individual ops.
var patchOps = map[string]toolOpEntry{
	"function": {
		Description: "Edit lines inside one or more function bodies (literal/regex match, at_line, set_signature, insert_*). Pass one item for a single-target edit, N items for an atomic batch.",
		Required:    []string{"target=function", "items[].file", "items[].identifier", "items[].patches"},
		Example:     `{"target": "function", "items": [{"file": "foo.go", "identifier": "Foo", "patches": [{"op": "replace", "match": "x", "replace": "y"}]}]}`,
	},
	"struct": {
		Description: "Edit one or more structs' field lists (add/remove/rename/retype/set_tag/set_doc). N items applied atomically.",
		Required:    []string{"target=struct", "items[].file", "items[].identifier", "items[].patches"},
		Example:     `{"target": "struct", "items": [{"file": "foo.go", "identifier": "User", "patches": [{"op": "add_field", "name": "ID", "type": "string"}]}]}`,
	},
	"interface": {
		Description: "Edit an interface's method set and regenerate its mock; atomic ops (add/remove/rename/retype/set_doc/embed). N items applied sequentially (early failures leave earlier items written).",
		Required:    []string{"target=interface", "items[].file", "items[].identifier", "items[].patches"},
		Example:     `{"target": "interface", "items": [{"file": "foo.go", "identifier": "Reader", "patches": [{"op": "add_method", "signature": "Close() error"}]}]}`,
	},
	"file": {
		Description: "Whole-file text substitution with AST safety; use for cross-function batch edits. N items applied sequentially.",
		Required:    []string{"target=file", "items[].file", "items[].patches"},
		Example:     `{"target": "file", "items": [{"file": "foo.go", "patches": [{"match": "oldName", "replace": "newName"}]}]}`,
	},
	"decl": {
		Description: "Edit the value of a top-level const or var (multi-line strings, error vars, …). N items applied sequentially.",
		Required:    []string{"target=decl", "items[].file", "items[].identifier", "items[].patches"},
		Example:     `{"target": "decl", "items": [{"file": "foo.go", "identifier": "banner", "patches": [{"op": "replace", "match": "v1", "replace": "v2"}]}]}`,
	},
}

var patchFunctionOps = map[string]toolOpEntry{
	"replace": {
		Description: "Replace text inside the function body. Matches are whitespace-normalized; disambiguate with occurrence when ambiguous.",
		Required:    []string{"file", "identifier", "patches[].op=replace", "patches[].match OR match_regex OR at_line/from_line+to_line", "patches[].replace"},
		Example:     `{"target": "function", "file": "foo.go", "identifier": "Foo", "patches": [{"op": "replace", "match": "x", "replace": "y"}]}`,
	},
	"insert_before": {
		Description: "Insert a line before the matched location inside the function body.",
		Required:    []string{"file", "identifier", "patches[].op=insert_before", "patches[].match OR at_line", "patches[].code"},
		Example:     `{"target": "function", "file": "foo.go", "identifier": "Foo", "patches": [{"op": "insert_before", "match": "return nil", "code": "log.Println(\"done\")"}]}`,
	},
	"insert_after": {
		Description: "Insert a line after the matched location inside the function body.",
		Required:    []string{"file", "identifier", "patches[].op=insert_after", "patches[].match OR at_line", "patches[].code"},
		Example:     `{"target": "function", "file": "foo.go", "identifier": "Foo", "patches": [{"op": "insert_after", "match": "x := 1", "code": "y := 2"}]}`,
	},
	"delete": {
		Description: "Delete the matched text (or line range) from the function body.",
		Required:    []string{"file", "identifier", "patches[].op=delete", "patches[].match OR at_line/from_line+to_line"},
		Example:     `{"target": "function", "file": "foo.go", "identifier": "Foo", "patches": [{"op": "delete", "match": "debug := true"}]}`,
	},
	"wrap": {
		Description: "Wrap the matched text with a template whose %s is substituted by the match (e.g. adding a guard).",
		Required:    []string{"file", "identifier", "patches[].op=wrap", "patches[].match", "patches[].wrap"},
		Example:     `{"target": "function", "file": "foo.go", "identifier": "Foo", "patches": [{"op": "wrap", "match": "doStuff()", "wrap": "if ok {\n\t%s\n}"}]}`,
	},
	"set_signature": {
		Description: "Rewrite only the params list and/or the returns of a function or method, leaving the body, name, receiver, and type parameters intact. Supply params as an array of declarations without parens (e.g. [\"ctx context.Context\", \"x int\"]) and/or returns; at least one is required.",
		Required:    []string{"file", "identifier", "patches[].op=set_signature", "patches[].params AND/OR patches[].returns"},
		Example:     `{"target": "function", "file": "foo.go", "identifier": "Foo", "patches": [{"op": "set_signature", "params": ["ctx context.Context", "x int"], "returns": "error"}]}`,
	},
}

var patchStructOps = map[string]toolOpEntry{
	"add_field": {
		Description: "Append or insert a field on the struct. Use before/after to anchor or position=first/last.",
		Required:    []string{"file", "identifier", "patches[].op=add_field", "patches[].name", "patches[].type"},
		Example:     `{"target": "struct", "file": "foo.go", "identifier": "User", "patches": [{"op": "add_field", "name": "ID", "type": "string"}]}`,
	},
	"remove_field": {
		Description: "Remove a field from the struct by name (use the type literal like 'io.Reader' for embedded fields).",
		Required:    []string{"file", "identifier", "patches[].op=remove_field", "patches[].name"},
		Example:     `{"target": "struct", "file": "foo.go", "identifier": "User", "patches": [{"op": "remove_field", "name": "ID"}]}`,
	},
	"rename_field": {
		Description: "Rename a struct field in-place (does not rewrite usages — pair with rename_symbol for that).",
		Required:    []string{"file", "identifier", "patches[].op=rename_field", "patches[].from", "patches[].to"},
		Example:     `{"target": "struct", "file": "foo.go", "identifier": "User", "patches": [{"op": "rename_field", "from": "Id", "to": "ID"}]}`,
	},
	"retype_field": {
		Description: "Change the type of an existing struct field.",
		Required:    []string{"file", "identifier", "patches[].op=retype_field", "patches[].name", "patches[].type"},
		Example:     `{"target": "struct", "file": "foo.go", "identifier": "User", "patches": [{"op": "retype_field", "name": "ID", "type": "int64"}]}`,
	},
	"set_tag": {
		Description: "Set or clear the struct tag on a field (tag content without backticks; empty string clears).",
		Required:    []string{"file", "identifier", "patches[].op=set_tag", "patches[].name", "patches[].tag"},
		Example:     `{"target": "struct", "file": "foo.go", "identifier": "User", "patches": [{"op": "set_tag", "name": "Email", "tag": "json:\"email,omitempty\""}]}`,
	},
	"set_doc": {
		Description: "Set or clear the doc comment on a field (empty string clears).",
		Required:    []string{"file", "identifier", "patches[].op=set_doc", "patches[].name", "patches[].doc"},
		Example:     `{"target": "struct", "file": "foo.go", "identifier": "User", "patches": [{"op": "set_doc", "name": "Email", "doc": "Email is the primary contact address."}]}`,
	},
}

var patchInterfaceOps = map[string]toolOpEntry{
	"add_method": {
		Description: "Append a method to the interface (signature without 'func'). Mock is regenerated in lockstep.",
		Required:    []string{"file", "identifier", "patches[].op=add_method", "patches[].signature"},
		Example:     `{"target": "interface", "file": "foo.go", "identifier": "Reader", "patches": [{"op": "add_method", "signature": "Close() error"}]}`,
	},
	"remove_method": {
		Description: "Remove a method from the interface by name. Mock is regenerated in lockstep.",
		Required:    []string{"file", "identifier", "patches[].op=remove_method", "patches[].name"},
		Example:     `{"target": "interface", "file": "foo.go", "identifier": "Reader", "patches": [{"op": "remove_method", "name": "Close"}]}`,
	},
	"rename_method": {
		Description: "Rename an interface method in-place. Does not rewrite call sites — pair with rename_symbol for that.",
		Required:    []string{"file", "identifier", "patches[].op=rename_method", "patches[].from", "patches[].to"},
		Example:     `{"target": "interface", "file": "foo.go", "identifier": "Reader", "patches": [{"op": "rename_method", "from": "Close", "to": "Shutdown"}]}`,
	},
	"retype_method": {
		Description: "Replace a method's signature (params/returns) on the interface; mock is regenerated.",
		Required:    []string{"file", "identifier", "patches[].op=retype_method", "patches[].name", "patches[].signature"},
		Example:     `{"target": "interface", "file": "foo.go", "identifier": "Reader", "patches": [{"op": "retype_method", "name": "Read", "signature": "Read(ctx context.Context, p []byte) (int, error)"}]}`,
	},
	"set_doc": {
		Description: "Set or clear the interface-level doc comment (empty string clears).",
		Required:    []string{"file", "identifier", "patches[].op=set_doc", "patches[].doc"},
		Example:     `{"target": "interface", "file": "foo.go", "identifier": "Reader", "patches": [{"op": "set_doc", "doc": "Reader reads bytes."}]}`,
	},
	"embed": {
		Description: "Embed another interface type inside this one (e.g. 'io.Reader'). Mock is regenerated.",
		Required:    []string{"file", "identifier", "patches[].op=embed", "patches[].name"},
		Example:     `{"target": "interface", "file": "foo.go", "identifier": "ReadCloser", "patches": [{"op": "embed", "name": "io.Reader"}]}`,
	},
	"remove_embed": {
		Description: "Remove an embedded interface by its type literal.",
		Required:    []string{"file", "identifier", "patches[].op=remove_embed", "patches[].name"},
		Example:     `{"target": "interface", "file": "foo.go", "identifier": "ReadCloser", "patches": [{"op": "remove_embed", "name": "io.Reader"}]}`,
	},
}

var patchDeclOps = map[string]toolOpEntry{
	"replace": {
		Description: "Replace text inside the const/var's value expression. String literals match inside quotes; other expressions match the full value text.",
		Required:    []string{"file", "identifier", "patches[].op=replace", "patches[].match OR match_regex OR at_line/from_line+to_line", "patches[].replace"},
		Example:     `{"target": "decl", "file": "foo.go", "identifier": "banner", "patches": [{"op": "replace", "match": "v1", "replace": "v2"}]}`,
	},
	"insert_before": {
		Description: "Insert a line before the matched location inside the value expression.",
		Required:    []string{"file", "identifier", "patches[].op=insert_before", "patches[].match OR at_line", "patches[].code"},
		Example:     `{"target": "decl", "file": "foo.go", "identifier": "banner", "patches": [{"op": "insert_before", "match": "hello", "code": "greeting: "}]}`,
	},
	"insert_after": {
		Description: "Insert a line after the matched location inside the value expression.",
		Required:    []string{"file", "identifier", "patches[].op=insert_after", "patches[].match OR at_line", "patches[].code"},
		Example:     `{"target": "decl", "file": "foo.go", "identifier": "banner", "patches": [{"op": "insert_after", "match": "hello", "code": " world"}]}`,
	},
	"delete": {
		Description: "Delete the matched text (or line range) from the value expression.",
		Required:    []string{"file", "identifier", "patches[].op=delete", "patches[].match OR at_line/from_line+to_line"},
		Example:     `{"target": "decl", "file": "foo.go", "identifier": "banner", "patches": [{"op": "delete", "match": "deprecated: "}]}`,
	},
	"wrap": {
		Description: "Wrap the matched text with a template whose %s is substituted by the match.",
		Required:    []string{"file", "identifier", "patches[].op=wrap", "patches[].match", "patches[].wrap"},
		Example:     `{"target": "decl", "file": "foo.go", "identifier": "banner", "patches": [{"op": "wrap", "match": "hello", "wrap": "[%s]"}]}`,
	},
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
		format := in.Format
		if format == "" {
			format = "text"
		}
		if format != "text" && format != "json" {
			return errorResult("describe_tool: format must be 'text' or 'json'"), nil, nil
		}
		if in.Name != "" && in.Category != "" {
			return errorResult("describe_tool: name and category are mutually exclusive"), nil, nil
		}
		if in.Name != "" {
			if tool, op, ok := splitToolOp(in.Name); ok {
				return renderDescribeOp(tool, op, format), nil, nil
			}
			return renderDescribeOne(in.Name, format), nil, nil
		}
		return renderDescribeList(in.Category, format), nil, nil
	})
}

// renderDescribeOne returns the single-tool detail view.
func renderDescribeOne(name, format string) *mcp.CallToolResult {
	for _, e := range toolCatalog {
		if e.Name == name {
			if format == "json" {
				return jsonResult(describeToolOutput{Tool: toolEntryJSON(e)})
			}
			var sb strings.Builder
			fmt.Fprintf(&sb, "%s (%s)\n", e.Name, e.Category)
			fmt.Fprintf(&sb, "  %s\n", e.Summary)
			if e.Example != "" {
				fmt.Fprintf(&sb, "  example: %s\n", e.Example)
			}
			if e.Related != "" {
				fmt.Fprintf(&sb, "  see also: %s\n", e.Related)
			}
			if len(e.Limitations) > 0 {
				sb.WriteString("  limitations:\n")
				for _, lim := range e.Limitations {
					fmt.Fprintf(&sb, "    - %s\n", lim)
				}
			}
			return textResult(sb.String())
		}
	}
	return errorResult(fmt.Sprintf("describe_tool: unknown tool %q (try describe_tool with no args to see the catalog)", name))
}

// renderDescribeList returns the grouped list view; when category is
// set, only entries from that group are emitted.
func renderDescribeList(category, format string) *mcp.CallToolResult {
	if category != "" {
		if _, ok := categoryLabels[category]; !ok {
			return errorResult(fmt.Sprintf("describe_tool: unknown category %q (valid: %s)", category, strings.Join(categoryOrder, ", ")))
		}
	}
	if format == "json" {
		tools := []toolEntryOut{}
		for _, cat := range categoryOrder {
			if category != "" && cat != category {
				continue
			}
			var rows []toolEntry
			for _, e := range toolCatalog {
				if e.Category == cat {
					rows = append(rows, e)
				}
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
			for _, r := range rows {
				tools = append(tools, toolEntryJSON(r))
			}
		}
		return jsonResult(describeToolListOutput{Tools: tools})
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

// toolOpEntry describes one operation within a multi-op tool (e.g. the
// "set_signature" op of patch_function). Populated on toolEntry.Ops for
// tools whose schemas branch on an `op` field.
type toolOpEntry struct {
	Description string
	Required    []string
	Example     string
}

// splitToolOp parses a `tool.op` name; returns tool, op, true when the
// suffix after the first '.' is present and non-empty. Returns ok=false
// for plain tool names without a dot.
func splitToolOp(name string) (string, string, bool) {
	idx := strings.IndexByte(name, '.')
	if idx <= 0 || idx == len(name)-1 {
		return name, "", false
	}
	return name[:idx], name[idx+1:], true
}

// renderDescribeOp emits help for a single `tool.op`. Text format by
// default; JSON format when format="json".
func renderDescribeOp(toolName, op, format string) *mcp.CallToolResult {
	var entry toolEntry
	found := false
	for _, e := range toolCatalog {
		if e.Name == toolName {
			entry = e
			found = true
			break
		}
	}
	if !found {
		return errorResult(fmt.Sprintf("describe_tool: unknown tool %q (try describe_tool with no args to see the catalog)", toolName))
	}
	if len(entry.Ops) == 0 {
		return errorResult(fmt.Sprintf("describe_tool: tool %q has no ops", toolName))
	}
	opEntry, ok := entry.Ops[op]
	if !ok {
		knownOps := sortedOpKeys(entry.Ops)
		return errorResult(fmt.Sprintf("describe_tool: unknown op %q on %s (known ops: %s)", op, toolName, strings.Join(knownOps, ", ")))
	}
	if format == "json" {
		return jsonResult(describeToolOpOutput{Tool: toolOpOut{
			Name:        entry.Name,
			Op:          op,
			Description: opEntry.Description,
			Required:    opEntry.Required,
			Example:     opEntry.Example,
		}})
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s.%s (%s)\n", entry.Name, op, entry.Category)
	fmt.Fprintf(&sb, "  %s\n", opEntry.Description)
	if len(opEntry.Required) > 0 {
		fmt.Fprintf(&sb, "  required: %s\n", strings.Join(opEntry.Required, ", "))
	}
	if opEntry.Example != "" {
		fmt.Fprintf(&sb, "  example: %s\n", opEntry.Example)
	}
	return textResult(sb.String())
}

// sortedOpKeys returns the op keys of an Ops map in a deterministic
// order — the order they were listed in the source catalog, falling
// back to alphabetic. Used to surface a stable "known ops" list to
// agents in error messages.
func sortedOpKeys(ops map[string]toolOpEntry) []string {
	// Preserve the canonical order declared per-tool when possible; the
	// roadmap specifies specific orderings (e.g. replace, insert_before,
	// insert_after, delete, wrap, set_signature for patch_function).
	canonical := canonicalOpOrder(ops)
	if canonical != nil {
		return canonical
	}
	keys := make([]string, 0, len(ops))
	for k := range ops {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// canonicalOpOrder returns the intended display order for one of the
// known op catalogs, or nil if the map is not one of them.
func canonicalOpOrder(ops map[string]toolOpEntry) []string {
	orders := [][]string{
		{"function", "struct", "interface", "file", "decl"},
		{"replace", "insert_before", "insert_after", "delete", "wrap", "set_signature"},
		{"add_field", "remove_field", "rename_field", "retype_field", "set_tag", "set_doc"},
		{"add_method", "remove_method", "rename_method", "retype_method", "set_doc", "embed", "remove_embed"},
		{"replace", "insert_before", "insert_after", "delete", "wrap"},
	}
	for _, order := range orders {
		if len(order) != len(ops) {
			continue
		}
		match := true
		for _, k := range order {
			if _, ok := ops[k]; !ok {
				match = false
				break
			}
		}
		if match {
			return order
		}
	}
	return nil
}

// toolEntryJSON projects an internal toolEntry onto the stable JSON
// shape exposed via StructuredContent.
func toolEntryJSON(e toolEntry) toolEntryOut {
	return toolEntryOut{
		Name:        e.Name,
		Category:    e.Category,
		Summary:     e.Summary,
		Example:     e.Example,
		Related:     e.Related,
		Limitations: e.Limitations,
	}
}

// jsonResult renders a StructuredContent payload as both the machine
// value and a compact JSON string body — stdio clients without a
// structured-content channel still see something readable.
func jsonResult(payload any) *mcp.CallToolResult {
	buf, err := json.Marshal(payload)
	if err != nil {
		return errorResult(fmt.Sprintf("describe_tool: failed to marshal json: %v", err))
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(buf)},
		},
		StructuredContent: payload,
	}
}

// toolEntryOut is the JSON projection of toolEntry (catalog row).
type toolEntryOut struct {
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Summary     string   `json:"summary"`
	Example     string   `json:"example,omitempty"`
	Related     string   `json:"related,omitempty"`
	Limitations []string `json:"limitations,omitempty"`
}

// describeToolOutput is the JSON shape for name=X (single tool).
type describeToolOutput struct {
	Tool toolEntryOut `json:"tool"`
}

// describeToolListOutput is the JSON shape for no-name (list).
type describeToolListOutput struct {
	Tools []toolEntryOut `json:"tools"`
}

// toolOpOut is the JSON projection of a single op's help.
type toolOpOut struct {
	Name        string   `json:"name"`
	Op          string   `json:"op"`
	Description string   `json:"description"`
	Required    []string `json:"required,omitempty"`
	Example     string   `json:"example,omitempty"`
}

// describeToolOpOutput is the JSON shape for name=X.op.
type describeToolOpOutput struct {
	Tool toolOpOut `json:"tool"`
}
