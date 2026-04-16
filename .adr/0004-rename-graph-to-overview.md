# ADR 0004 — Rename `graph` tool to `overview`

## Status
Accepted — 2026-04-16.

## Context
Pass-1 trace analysis (run-33-surgeon): 147 `symbol` calls, ~0 `graph`
calls. Agents chain `symbol` to reconstruct package-level context
instead of reaching for the one-call primitive (`graph`) that would
give them the shape of the codebase directly.

Hypothesis: the name `graph` primes agents for "dependency graph /
edge-and-node data." They don't reach for it when the intent is "show
me what's in this project." The right word is `overview` — what you'd
say to a colleague on their first day.

## Decision
Rename `graph` → `overview` in the MCP tool registry. No alias, no
deprecation period (per user's earlier instruction: "do not keep graph
alias, just rename it").

Domain types (`GraphOptions`, `GraphPackage`, etc.) stay as-is — they
describe an internal data structure, not an interface. Only the user-
facing MCP tool name changes:
- MCP tool name: `graph` → `overview`
- Tool description rewritten as a prompt ("use this first when you're
  dropped into an unfamiliar codebase; returns the package tree,
  optionally with per-file symbol signatures").

**Scope:** MCP only. The CLI subcommand `go-surgeon graph` stays as
`graph` — CLI users may have scripts pinned to the current name, and
the CLI/MCP mismatch is acceptable given their audiences are
different (MCP = LLM agents who read descriptions on every call;
CLI = humans who memorize command names). Revisit if the CLI gets a
major version bump.

## Alternatives considered

### A. `map` — "what's in this area"
Rejected. Too generic / overloaded (Go maps, mental maps, map() in
other languages). Agents would need to read the description every time
to disambiguate.

### B. `outline` — parallel with symbol's context=file outline
Rejected. Confusing collision. `symbol context=file` already returns
an outline of one file; a separate `outline` tool at the package level
is the kind of naming that makes agents guess.

### C. `inventory` / `survey`
Rejected. Accurate but uncommon in Go/CLI tooling. Agents don't type
these words when thinking about "what's in this project."

### D. Keep `graph`, rewrite description to emphasize "start here"
Rejected. Tried indirectly in current description ("Reach for this
first on an unfamiliar codebase"). Agents still skip it. The name is
primed wrong; no amount of description recovery closes that.

## Consequences
**Positive**
- Agents naturally reach for `overview` when their intent is "show me
  the project." No more 147-symbol-call exploration chains.
- Clear name hierarchy: `overview` (project) → `symbol context=file`
  (file) → `symbol` (one decl).

**Negative**
- Breaking change for anyone scripting against `graph` by name.
  Mitigated by: (a) project is pre-1.0, (b) user explicitly wanted no
  alias.
