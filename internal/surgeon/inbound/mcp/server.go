package mcp

import (
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverInstructions is the always-loaded system prompt for clients
// connecting to the go-surgeon MCP server. It is intentionally short:
// the full mental model and per-tool documentation live in the
// `go-surgeon` CLI, fetched on demand to avoid permanent context bloat.
const serverInstructions = `go-surgeon is the AST-aware editor for Go files. It replaces Read/Edit/Write/Grep/Glob/Bash for anything that touches a .go file — including reading Go code. Don't start with go-surgeon and then fall back to Grep mid-task.

Workflow: EXPLORE (overview, symbol) → EDIT (patch is the narrow default; update for whole-decl rewrites; create / delete; interface for interface+mock; execute_plan for 3+ atomic edits) → VALIDATE (build_check, test_run).

For per-tool detail, examples, ops, and known limitations, shell out to:
  go-surgeon discovery              # grouped catalog
  go-surgeon discovery <tool>       # tool detail
  go-surgeon discovery <tool>.<op>  # op detail (e.g. patch.function)

To install go-surgeon as a Claude skill in this project:
  go-surgeon skill --out .claude/skills/go-surgeon/

Universal rules:
- ` + "`content`" + ` is raw Go: never include 'package ...' or 'import (...)' blocks — goimports runs after every edit.
- Always read with symbol body=true before update/delete.
- identifier forms: 'FuncName', 'Receiver.Method', 'StructName'/'InterfaceName', 'pkg.Name'.
- symbol uses 'query' for lookup; find_references and find_definition use 'name' — they are different parameters.
- update: 'object' is optional and defaults to 'auto' (inferred from content).`

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

	s.AddReceivingMiddleware(schemaHintMiddleware())

	return s
}
