# ADR 0005 — Schema-hint middleware for patches-as-string

## Status
Accepted — 2026-04-16.

## Context
Pass-1 C/D/F reported that when an MCP client (intermittently)
serializes the `patches` field as a JSON-encoded string rather than a
JSON array, the MCP SDK rejects the call with:

    validating "arguments": type: [...] has type "string", want one of "null, array"

This message is opaque to the LLM agent on the other side. It surfaces
a schema validator internal, says nothing about the likely cause
(double-serialization by the client), and suggests no recovery.

The v2 roadmap (#3 in `.feedbacks/roadmap-v2.md`) asks for an
actionable error on this specific input pattern.

The complication: the SDK validates `req.Params.Arguments` against the
tool's input schema *before* invoking our handler. On failure, it
returns an `IsError=true` `CallToolResult` directly. There is no
"first line of the handler" where we can intercept — our code
never runs.

## Decision
Add a **server-level receiving middleware** that runs before per-tool
dispatch (and therefore before SDK schema validation). The middleware:

1. Matches only the `tools/call` method.
2. Matches only tool names in a hard-coded whitelist
   (`patch_function`, `patch_struct`, `patch_interface`).
3. Inspects `req.Params.Arguments` as raw JSON: if the top-level
   `patches` field is a JSON string (not an array), short-circuits
   with a descriptive `*CallToolResult{IsError: true}` and a message
   that names the root cause and suggests a recovery path.
4. Distinguishes "the inner string is itself a JSON array"
   (double-serialization — the real bug) from "the inner string is
   unrelated text." Only the former gets the "serialized twice"
   wording.
5. Falls through to the default handler on any miss.

Implementation:
- `internal/surgeon/inbound/mcp/schema_hint.go` — the middleware and
  helpers.
- One line in `NewServer`:
  `s.AddReceivingMiddleware(schemaHintMiddleware())`, placed after all
  tools are registered.

## Alternatives considered

### A. Pre-check inside each typed handler
Rejected. The SDK's generic `mcp.AddTool` validates before the typed
handler runs. Our code never executes on a schema-invalid payload, so
there is nothing to pre-check from inside the handler.

### B. Replace `mcp.AddTool` (generic) with `s.AddTool` (low-level) for patch tools
Rejected. The generic `AddTool` does significant work: schema
inference from the In type, defaults application, output marshaling,
typed-handler dispatch. Reimplementing all of this for three tools to
customize one error message duplicates SDK internals, couples us to
SDK version changes, and risks silent behavior drift on upgrade.

### C. Schema patching (describe `patches` with a custom validator description)
Rejected. JSON Schema `description` appears on the tool's advertised
input schema, which helps the LLM *before* it sends a bad call, but
does nothing to improve the error message *after*. The roadmap
specifically cited the error quality; the description route solves a
different problem.

### D. Post-response middleware to rewrite SDK error strings
Rejected. The error returned by the SDK validator is embedded in a
`CallToolResult.Content[0].TextContent.Text` with the prefix
`validating "arguments": ...`. Rewriting it after-the-fact requires
string-matching the exact SDK message, which is brittle across SDK
versions, and requires parsing the original `Arguments` anyway to
confirm the diagnosis. Pre-check is strictly simpler.

## Consequences

**Positive**
- The agent gets an actionable error naming the bug and suggesting
  a retry/fallback path, instead of a validator internal.
- Zero impact on happy-path requests — middleware is a whitelist
  check then pass-through.
- No coupling to the generic `AddTool` / typed-handler internals.
  If the SDK changes its schema-validator error format, our code is
  unaffected.

**Negative**
- Only the `patches`-as-string mismatch gets an actionable message.
  Other schema errors still surface via the SDK's default validator.
  Acceptable: this is the only schema mismatch observed in
  dog-fooding, and a general schema-error rewriter would require
  re-implementing the validator.
- The whitelist of patch-tool names is hard-coded. If a new tool
  grows a `patches` field, it must be added. Acceptable: the set of
  patch-tools has been stable for 4+ passes, and a lint could enforce
  the invariant if it starts to drift.

## Scope
MCP server only. The CLI does not hit this path — CLI args are
positional/flag-parsed, never a JSON array under a `patches` key.
