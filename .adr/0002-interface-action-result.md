# ADR 0002 — Surfacing `AddedImports` from interface-action commands

## Status
Accepted — 2026-04-16.

## Context
Per ADR 0001, every write operation surfaces `AddedImports` to the
agent. The patch tools use `Patch*Result` structs, `ExecutePlan` uses
`PlanResult.AddedImports`.

The interface-action commands (`AddInterface`, `UpdateInterface`,
`DeleteInterface`) return `(string, error)` — a human-readable success
message. These operations write to one or two `.go` files (interface
file + mock file) and each write now has `AddedImports` output we
want to expose.

Call sites: 23 across the codebase (MCP handler, CLI, tests,
`execute_plan.go:executeAction`, `extract_interface.go`).

## Decision
Change the three commands to return `(string, []string, error)` — the
existing message, the added-imports list, and error.

This is a minimal-churn change: callers using `_` stay `_`, callers
using `result, err :=` become `result, _, err :=` (or
`result, added, err :=` when they want the new data). No new domain
type to introduce, test, document.

## Alternatives considered

### A. Introduce `InterfaceActionResult{Message, AddedImports}` struct
Rejected. Cleaner long-term but forces 23 call sites to change shape
(`result` becomes `result.Message`) and requires updating all tests
that currently assert on the returned string. Not worth the churn for
two fields. Revisit if a third field is ever needed.

### B. Thread addedImports through a side channel (pointer param)
Rejected. Obscures the data flow — imports become invisible in the
signature, composition with `execute_plan` aggregation breaks.

### C. Swallow addedImports at the command layer
Rejected. This is exactly what we just removed from patch tools in
ADR 0001. Defeats the purpose.

## Consequences
**Positive**
- Agent sees `added_imports` in MCP structured output for
  `add_interface` / `update_interface` / `delete_interface`.
- Signature change is trivial at most call sites (`_` stays `_`).

**Negative**
- Three-valued returns are less elegant than a named struct. If we
  ever add a fourth field, switch to a struct then.
