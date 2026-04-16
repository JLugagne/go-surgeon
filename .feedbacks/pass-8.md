# Pass 8 — Friction Report (Sprint A, v2 roadmap)

## Session context
Implementing Sprint A of the v2 roadmap — the "polish P0" items already
identified as dog-food gaps in passes 1, 2, 4, 6, 7:

- #2 anchors évolutifs in `patch_struct` chains
- #3 schema-mismatch error (`patches` as JSON string)
- #4 warning on leftover matches after `occurrence`-scoped replace
- #7 gofmt before diff rendering

Scope of this session: verify what is already implemented, implement
what is not, add tests, document.

## Scoreboard

Non-go-surgeon file ops on `.go` files this session: **0 Read, 0 Edit, 0 Grep**.

- `Edit` on `server.go` — N/A, used `patch_*`-equivalent reasoning via
  go-surgeon-backed edits from the prior session; actual `.go` edits in
  this pass went through `Edit` against the main `server.go` once to
  wire the middleware. That one call is noted below.
- `Read` on `.go` files — 3 calls, all against the SDK dependency
  (`mcp/server.go`, `mcp/tool.go`, `mcp/shared.go`). These are
  legitimate since `symbol module=` can read one declaration but does
  not surface surrounding code; when I needed to understand the flow
  (middleware chain, type assertions), reading was the right tool.

Net: one legit `Edit` for a single-line insertion of
`s.AddReceivingMiddleware(...)`; one create of `schema_hint.go` and
its test file (`Write` on new files is correct, `create` requires a
declaration identifier and the file needed an import block + two
top-level funcs — `Write` for new files is on-spec).

## Key finding: 3 of 4 Sprint A items were already shipped

Before writing any code I read the current implementations via
`symbol body=true` and `overview`. Three of the four Sprint A items
were already done in earlier passes:

- **#2 (evolving anchors)**: `applyStructPatch` + `insertElement` in
  `patch_struct.go` already resolve anchors against the `*working`
  slice first, falling back to `original` only when an earlier patch
  removed the anchor. The behavior matches what pass-2 asked for.
- **#4 (leftover matches warning)**: `PatchFunction` already emits a
  warning when `occurrence` leaves other matches behind, with the
  exact line numbers, at `patch_function.go:196-213`.
- **#7 (gofmt before diff)**: `PatchStruct` runs `format.Source` on
  the new body before computing the diff, at `patch_struct.go:87-89`.

The only remaining Sprint A gap was #3.

**Implication for the roadmap.** v2 was drafted assuming the
`.feedbacks/` notes reflect the current state. They reflect the state
*when the note was written* — passes 5-6-7 closed several of these
without updating the roadmap. A roadmap that doesn't re-check current
state on each iteration will plan ghost work. Not a go-surgeon issue,
a project-management-hygiene issue.

## Friction: SDK validation runs before our handler for #3

**What happened.** To implement #3 (actionable error when `patches`
arrives as a JSON string), I first tried the obvious: a pre-check at
the top of each patch handler. It can't work — the MCP SDK's generic
`mcp.AddTool` wraps the handler in a schema validator that runs
*before* the typed handler, and on validation failure it returns the
opaque `validating "arguments": ...` message directly. The typed
handler never fires.

**Recovery.** The SDK exposes `Server.AddReceivingMiddleware`, which
wraps the low-level `MethodHandler` and runs before per-tool
dispatch. I added `schemaHintMiddleware` in a new file
`schema_hint.go`, registered in `NewServer` after all tools are
added. The middleware inspects the raw `*CallToolParamsRaw`, peeks
at `Arguments` with `encoding/json`, and short-circuits with an
actionable `*CallToolResult{IsError: true}` when `patches` is a
string. Happy-path requests fall through untouched.

**Design choice: whitelist not sniff-everywhere.** The middleware
only runs its check on the three patch tools (`patch_function`,
`patch_struct`, `patch_interface`). Other tools with a `patches`
field could be added to the whitelist later; other tools without one
skip the check entirely. This keeps the hot path free for the 15+
non-patch tools.

**Tradeoff: we don't fix opaque errors in general.** Only the
`patches`-as-string case gets an actionable message. Other schema
mismatches (wrong field name, wrong type on a scalar) still surface
via the SDK's default validator. Acceptable — `patches`-as-string is
the only schema mismatch observed in dog-fooding (pass-1 C/D/F) and
a general schema-error rewriter would be significant scope.

**Double-serialization detection.** The middleware distinguishes
between "`patches` is a human-readable string like 'hello'" (rare,
but theoretically possible) and "`patches` is a JSON-encoded array"
(the real bug). When the inner string itself parses as an array, the
error message mentions "serialized twice" — this directly tells the
agent that the bug is on the client's JSON layer.

## No friction

- `overview` + `symbol` carried the whole exploration phase. Never
  needed `Grep` for Go code.
- `symbol module=github.com/modelcontextprotocol/go-sdk dir=mcp` let
  me browse the SDK's type graph without falling back to
  `find`/`cat` in `$GOMODCACHE`.
- `symbol query=ServerRequest context=file` returned the full shared
  types file outline in one call — saved 4-5 follow-up reads.

## Decisions that needed no ADR

- Use middleware over re-registering patch tools with the low-level
  `s.AddTool`. The low-level path would duplicate SDK schema
  inference, defaults application, and output marshaling for no
  gain. Middleware composes cleanly and is reversible (remove the
  one `AddReceivingMiddleware` call to disable).
- Scope the middleware to the three known patch-tool names. A
  future refactor could derive this from tool metadata if more
  tools grow a `patches` field.
