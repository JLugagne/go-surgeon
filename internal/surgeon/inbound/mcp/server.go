package mcp

import (
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
- Changing a few lines inside one function body      → patch target=function (op=replace/insert_before/insert_after/delete)
- Multi-line replacement OR restructuring a func     → update object=func (NOT patch op=replace — op=replace is fragile across line boundaries)
- Same rename across many functions in one file      → patch target=file (bulk text substitution with AST safety)
- Editing the VALUE of a top-level const or var      → patch target=decl (multi-line string const, error var, etc.)
- Single field change on a struct                    → patch target=struct
- Single method change on an interface               → patch target=interface (regenerates the mock too via mock_file+mock_name)
- Inserting one statement at a fixed position        → insert_call
- Whole-declaration replacement (func/struct/file)   → update
- Adding a brand-new declaration (func/struct/file)  → create
- Removing a declaration                             → delete
- Adding an interface WITH its mock in one step      → interface action=add (set mock_file + mock_name)
- Updating or deleting an interface                  → interface action=update / action=delete (keep the mock in sync via mock_file/mock_name/delete_mock)
- Several coordinated edits                          → execute_plan (atomic, up to 15 actions)

WHEN TO BATCH WITH execute_plan
Principle: if you're about to make 3+ related edits that must land together, use execute_plan — one atomic call with rollback on failure.
- Example A (same change to two interfaces): bundle two patch_interface actions in one execute_plan (patch_interface actions carry their ops via patch_interface_ops). This is two round-trips as separate calls AND if the second fails the file is left with only one interface updated; the bundled form rolls both back on failure.
- Example B (new interface + implementation + test stub): one execute_plan with add_interface + add_struct (or create_file for the impl) + create_file for the _test.go lands the whole vertical slice atomically; a partial failure rolls back, so you never commit a half-wired type. (Inside execute_plan the action type is still 'add_interface'; the standalone tool is 'interface' with action=add.)
When NOT to use it: single-object edits don't need it. Don't reach for execute_plan just to feel safer — the granular patch target=... form already preserves everything you didn't touch.

Why the granular targets matter: re-emitting a whole function or struct via update forces you to reproduce the entire body, which is a common source of subtle drift (lost comments, reordered fields, missed branches). patch target=function/decl/struct/interface edit in place and preserve everything you didn't explicitly change.

VALIDATE (after you edit, to confirm the change is sound)
- build_check: runs 'go build' scoped to a package or directory (default './...') and returns structured diagnostics (file, line, column, message) deduplicated per file. Call this after any edit that could affect compilation instead of asking the user to run 'go build' or shelling out. Set tests=true to also compile test files. timeout_seconds caps the run (default 60, max 600). 'go vet' is out of scope; use build_check only for compile errors.
- test_run: runs 'go test' for a package/directory and reports pass/fail plus failing test output.

INTERFACE WORKFLOWS
- To add ONE method to an existing interface: use patch target=interface op=add_method. There is no add_interface_method tool, and interface action=update is overkill here.
- To restructure an interface significantly: interface action=update with the complete new declaration.
- scaffold kind=impl_from_interface: generate method stubs on a struct for an interface it doesn't yet satisfy.
- scaffold kind=mock_from_interface: generate a standalone mock for an interface you don't own (stdlib/third-party). For interfaces you own, prefer interface action=add + mock_file.
- scaffold kind=interface_from_type: scaffold an interface from an existing struct's exported methods (optionally also generates a mock via mock_file + mock_name).

CODE GENERATION
- test: generate a table-driven test skeleton for a function or method.
- struct tags: use patch target=struct op=auto_tag format=json|bson to bulk-generate snake_case tags on every exported field; use op=set_tag for a single field.

VALIDATE (after editing, before declaring the task done)
- test_run: run 'go test' scoped to a package/directory and get a compact pass/fail report with per-test timing and failure file:line references. Prefer this over shelling out to go test yourself. Pair with build_check for compile-time validation.

PATCH SHAPE
- patch accepts both shapes: single-target (top-level file + identifier + patches) for one declaration — the common case; or items: [{file, identifier, patches, ...}] for batch edits across N targets. Pick exactly one per call; mixing the two is rejected.

ERROR HINTS
- The patch_* tools ('patches' field) are guarded by a pre-validation hint. If a client accidentally sends 'patches' as a JSON-encoded string instead of an array, you'll get an explicit ERROR message naming the cause ('JSON-encoded string instead of an array', and 'serialized twice' when the inner string itself parses as an array) before the SDK's opaque schema error fires. When you see that message: resend 'patches' as a raw JSON array (not a stringified one), or fall back to update / interface action=update / update_struct with the full replacement declaration.
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
	registerExecutePlanTool(s, commands)
	registerCodegenTools(s, commands)
	registerPatchTools(s, commands)
	registerReferencesTools(s, queries)
	registerBatchQueryTool(s, queries)
	registerRenameTool(s, commands)
	registerDescribeTool(s)

	s.AddReceivingMiddleware(schemaHintMiddleware())

	return s
}
