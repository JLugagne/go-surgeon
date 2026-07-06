// Package discovery is the single source of truth for go-surgeon's
// tool catalog. Both the `go-surgeon discovery` CLI and the
// `go-surgeon skill` generator render from these structures, so MCP
// tool descriptions can stay short and point agents to the CLI for
// detail instead of inflating the always-loaded server instructions.
package discovery

// ToolEntry is one row in the tool catalog.
type ToolEntry struct {
	Name        string
	Category    string // explore | edit | validate | interface | codegen | refs | batch
	Summary     string
	Example     string
	Related     string
	Limitations []string
	Ops         map[string]ToolOpEntry
}

// ToolOpEntry describes one operation within a multi-op tool (e.g. the
// "set_signature" op of patch target=function). Populated on
// ToolEntry.Ops for tools whose schemas branch on an `op` field.
type ToolOpEntry struct {
	Description string
	Required    []string
	Example     string
}

// Catalog lists every go-surgeon tool. Order is intentional within a
// category (narrowest edit first, etc.).
var Catalog = []ToolEntry{
	// EXPLORE
	{Name: "overview", Category: "explore", Summary: "list packages + symbols across a project; START HERE on unfamiliar codebases", Example: `{"dir": "internal", "symbols": true}`, Related: "symbol"},
	{Name: "symbol", Category: "explore", Summary: "read one declaration (query='Name'/'Receiver.Method') or list matches by regex (pattern='...')", Example: `{"query": "NewServer", "body": true}`, Related: "overview, find_definition"},

	// REFS
	{Name: "find_definition", Category: "refs", Summary: "type-aware: locate a symbol's declaration across packages", Example: `{"name": "NewServer"}`, Related: "symbol, find_references"},
	{Name: "find_references", Category: "refs", Summary: "type-aware: list every use of a symbol, cross-package, deduplicated", Example: `{"name": "NewServer", "include_definition": true}`, Related: "find_definition, rename_symbol"},
	{Name: "rename_symbol", Category: "refs", Summary: "type-aware: rename a symbol and every reference; blocks export-status flips and in-scope collisions; preview=true for dry run", Example: `{"name": "OldName", "new_name": "NewName", "preview": true}`, Related: "find_references"},

	// EDIT
	{Name: "patch", Category: "edit", Summary: "edit Go source by target: function (body lines), struct (fields), interface (methods+mock), file (whole-file substitution), decl (const/var values). Dual shape: single-target (top-level file+identifier+patches) for one declaration, or items: [{file, identifier, patches, ...}] for batch (function/struct atomic; interface/file/decl sequential).", Example: `{"target": "function", "file": "foo.go", "identifier": "Foo", "patches": [{"op": "replace", "match": "x", "replace": "y"}]}`, Related: "update, execute_plan", Limitations: []string{
		"multi-line replacement: op=replace can mis-splice multi-line replacements (issues #3, #14) — patch validates results post-splice and refuses with PATCH_REPLACE_NOT_APPLIED or PATCH_DROPPED_CONTENT when content is dropped, leaving the file unchanged. Workaround: use 'update object=func' (or update object=file/struct) with the full new declaration",
		"replacement containing tabs/escapes: literal tab characters and escape sequences inside a multi-line replace value can confuse the splice — workaround: use 'update object=func' which takes raw Go source verbatim",
		"large struct-literal field insertion: inserting many fields into a big struct literal via op=replace is fragile — workaround: use 'update object=func' to rewrite the whole declaration that contains the literal",
	}, Ops: PatchOps},
	{Name: "patch.function", Category: "edit", Summary: "ops for target=function: replace, insert_before, insert_after, delete, wrap, set_signature", Ops: PatchFunctionOps},
	{Name: "patch.struct", Category: "edit", Summary: "ops for target=struct: add_field, remove_field, rename_field, retype_field, set_tag, set_doc, auto_tag", Ops: PatchStructOps},
	{Name: "patch.interface", Category: "edit", Summary: "ops for target=interface: add_method, remove_method, rename_method, retype_method, set_doc, embed, remove_embed", Ops: PatchInterfaceOps},
	{Name: "patch.decl", Category: "edit", Summary: "ops for target=decl: replace, insert_before, insert_after, delete, wrap", Ops: PatchDeclOps},
	{Name: "insert_call", Category: "edit", Summary: "insert one statement at a marked position inside a function body (before-return / end-of-body / after:<marker>)", Example: `{"file": "foo.go", "function": "Handler", "call": "log.Println(\"hi\")", "position": "after:TODO insert setup here"}`, Related: "patch"},
	{Name: "create", Category: "edit", Summary: "add a new file, function, or struct (object=file|func|struct)", Example: `{"object": "func", "file": "foo.go", "content": "func Foo() {}"}`, Related: "update, execute_plan"},
	{Name: "update", Category: "edit", Summary: "whole-declaration replacement (replace_file / update_func / update_struct); prefer patch when editing in place", Example: `{"object": "func", "file": "foo.go", "identifier": "Foo", "content": "func Foo() {}"}`, Related: "patch, create"},
	{Name: "delete", Category: "edit", Summary: "remove a function, method, or struct (object=func|struct)", Example: `{"object": "func", "file": "foo.go", "identifier": "Foo"}`, Related: "interface"},

	// INTERFACE
	{Name: "interface", Category: "interface", Summary: "manage interfaces and their mocks atomically (action=add|update|delete). add creates interface + mock; update replaces the declaration and keeps the mock in sync; delete removes the interface and optionally the mock.", Example: `{"action": "add", "file": "foo.go", "identifier": "Reader", "content": "type Reader interface { Read(p []byte) (int, error) }", "mock_file": "mock_reader.go", "mock_name": "MockReader"}`, Related: "patch, scaffold", Limitations: []string{
		"action=add: requires file + content (mock_file + mock_name optional → also generates the mock). Example: {\"action\": \"add\", \"file\": \"foo.go\", \"identifier\": \"Reader\", \"content\": \"type Reader interface { Read(p []byte) (int, error) }\", \"mock_file\": \"mock_reader.go\", \"mock_name\": \"MockReader\"}",
		"action=update: requires file + identifier; pass content to rewrite the body, doc to set the doc comment, or strip_doc=true to remove it. mock_file + mock_name regenerate the mock. Example: {\"action\": \"update\", \"file\": \"foo.go\", \"identifier\": \"Reader\", \"content\": \"type Reader interface { Read(p []byte) (int, error); Close() error }\", \"mock_file\": \"mock_reader.go\", \"mock_name\": \"MockReader\"}",
		"action=delete: requires file + identifier; pass delete_mock=true with mock_file + mock_name to also remove the mock. Example: {\"action\": \"delete\", \"file\": \"foo.go\", \"identifier\": \"Reader\", \"delete_mock\": true, \"mock_file\": \"mock_reader.go\", \"mock_name\": \"MockReader\"}",
	}},

	// CODEGEN
	{Name: "scaffold", Category: "codegen", Summary: "generate code derived from an existing symbol; kind selects the operation: interface_from_type | impl_from_interface | mock_from_interface", Example: `{"kind": "impl_from_interface", "file": "foo.go", "source": "io.Reader", "target": "*Foo"}`, Related: "interface", Limitations: []string{
		"kind=interface_from_type: scaffold an interface from a struct's exported methods. Required: file, identifier (struct), target (new interface name). Optional: out, mock_file + mock_name (also generate a mock). Example: {\"kind\": \"interface_from_type\", \"file\": \"service.go\", \"identifier\": \"Service\", \"target\": \"ServiceAPI\", \"out\": \"iface.go\"}",
		"kind=impl_from_interface: generate method stubs on a receiver to satisfy an interface. Required: file, source (FQN interface), target (receiver, e.g. *MyStruct). Example: {\"kind\": \"impl_from_interface\", \"file\": \"reader.go\", \"source\": \"io.Reader\", \"target\": \"*MyReader\"}",
		"kind=mock_from_interface: generate a function-field mock for an interface you don't own. Required: file, source (FQN interface), target (mock struct name). Example: {\"kind\": \"mock_from_interface\", \"file\": \"mocks/writer.go\", \"source\": \"io.Writer\", \"target\": \"MockWriter\"}",
	}},
	{Name: "test", Category: "codegen", Summary: "generate a table-driven test skeleton for a function or method", Example: `{"file": "foo.go", "identifier": "Foo"}`, Related: "test_run"},

	// VALIDATE
	{Name: "build_check", Category: "validate", Summary: "run go build and return structured compile diagnostics; affected_by=file narrows to that file's reverse-dep closure", Example: `{"affected_by": "internal/foo/bar.go"}`, Related: "test_run"},
	{Name: "test_run", Category: "validate", Summary: "run go test and return a compact pass/fail report; affected_by=file narrows to owning package + reverse-deps; verbosity=summary|full controls payload size (auto-summary above 50 tests)", Example: `{"affected_by": "internal/foo/bar.go", "verbosity": "summary"}`, Related: "build_check, test"},

	// BATCH
	{Name: "execute_plan", Category: "batch", Summary: "apply up to 15 edit actions atomically (create/update/delete/patch_*); use when 3+ edits must land together or roll back together", Example: `{"actions": [{"action": "patch_function", "file": "a.go", "identifier": "Foo", "patch_function_ops": [{"op": "replace", "match": "x", "replace": "y"}]}]}`, Related: "patch, create"},
	{Name: "batch_query", Category: "batch", Summary: "run up to 10 read-only queries (symbol/overview/find_references/find_definition) in one round-trip; fail-soft per item", Example: `{"queries": [{"op": "overview", "focus": "internal"}, {"op": "symbol", "query": "NewServer"}]}`, Related: "symbol, overview"},
}

// PatchOps documents the target discriminator on patch.
var PatchOps = map[string]ToolOpEntry{
	"function": {
		Description: "Edit lines inside a function body (literal/regex match, at_line, set_signature, insert_*). Single-target shape shown; wrap multiple in items: [{...}] for an atomic batch.",
		Required:    []string{"target=function", "file", "identifier", "patches"},
		Example:     `{"target": "function", "file": "foo.go", "identifier": "Foo", "patches": [{"op": "replace", "match": "x", "replace": "y"}]}`,
	},
	"struct": {
		Description: "Edit a struct's field list (add/remove/rename/retype/set_tag/set_doc/auto_tag). Single-target shape shown; wrap multiple in items: [{...}] for an atomic batch. auto_tag (bulk-generate tags for every exported field; json emits camelCase, other formats emit snake_case) cannot be combined with other ops in the same call.",
		Required:    []string{"target=struct", "file", "identifier", "patches"},
		Example:     `{"target": "struct", "file": "foo.go", "identifier": "User", "patches": [{"op": "auto_tag", "format": "json"}]}`,
	},
	"interface": {
		Description: "Edit an interface's method set and regenerate its mock; atomic ops (add/remove/rename/retype/set_doc/embed). Single-target shape shown; wrap multiple in items: [{...}] for sequential batch.",
		Required:    []string{"target=interface", "file", "identifier", "patches"},
		Example:     `{"target": "interface", "file": "foo.go", "identifier": "Reader", "patches": [{"op": "add_method", "signature": "Close() error"}]}`,
	},
	"file": {
		Description: "Whole-file text substitution with AST safety; use for cross-function batch edits. Single-target shape shown; wrap multiple in items: [{...}] for sequential batch.",
		Required:    []string{"target=file", "file", "patches"},
		Example:     `{"target": "file", "file": "foo.go", "patches": [{"match": "oldName", "replace": "newName"}]}`,
	},
	"decl": {
		Description: "Edit the value of a top-level const or var (multi-line strings, error vars, …). Single-target shape shown; wrap multiple in items: [{...}] for sequential batch.",
		Required:    []string{"target=decl", "file", "identifier", "patches"},
		Example:     `{"target": "decl", "file": "foo.go", "identifier": "banner", "patches": [{"op": "replace", "match": "v1", "replace": "v2"}]}`,
	},
}

var PatchFunctionOps = map[string]ToolOpEntry{
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

var PatchStructOps = map[string]ToolOpEntry{
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
	"auto_tag": {
		Description: "Bulk-generate struct tags for every exported field of the struct using the given format: 'json' emits camelCase names, other formats (e.g. 'bson') emit snake_case. For a single field, use set_tag instead. Cannot be combined with other ops in the same patch call (split into two calls).",
		Required:    []string{"file", "identifier", "patches[].op=auto_tag", "patches[].format"},
		Example:     `{"target": "struct", "file": "foo.go", "identifier": "User", "patches": [{"op": "auto_tag", "format": "json"}]}`,
	},
}

var PatchInterfaceOps = map[string]ToolOpEntry{
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
		Description: "Set or clear the doc comment on a named member (method or embed) of the interface (empty string clears). Not interface-level — patches[].name selects which member.",
		Required:    []string{"file", "identifier", "patches[].op=set_doc", "patches[].name", "patches[].doc"},
		Example:     `{"target": "interface", "file": "foo.go", "identifier": "Reader", "patches": [{"op": "set_doc", "name": "Read", "doc": "Read reads bytes."}]}`,
	},
	"embed": {
		Description: "Embed another interface type inside this one (e.g. 'io.Reader'). Mock is regenerated.",
		Required:    []string{"file", "identifier", "patches[].op=embed", "patches[].type"},
		Example:     `{"target": "interface", "file": "foo.go", "identifier": "ReadCloser", "patches": [{"op": "embed", "type": "io.Reader"}]}`,
	},
	"remove_embed": {
		Description: "Remove an embedded interface by its type literal.",
		Required:    []string{"file", "identifier", "patches[].op=remove_embed", "patches[].type"},
		Example:     `{"target": "interface", "file": "foo.go", "identifier": "ReadCloser", "patches": [{"op": "remove_embed", "type": "io.Reader"}]}`,
	},
}

var PatchDeclOps = map[string]ToolOpEntry{
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

// CategoryOrder controls the order groups appear in rendered output.
var CategoryOrder = []string{"explore", "refs", "edit", "interface", "codegen", "validate", "batch"}

// CategoryLabels maps catalog codes to the prose labels.
var CategoryLabels = map[string]string{
	"explore":   "EXPLORE",
	"refs":      "REFS & RENAME",
	"edit":      "EDIT",
	"interface": "INTERFACE (interface + mock lockstep)",
	"codegen":   "CODE GENERATION",
	"validate":  "VALIDATE",
	"batch":     "BATCH",
}
