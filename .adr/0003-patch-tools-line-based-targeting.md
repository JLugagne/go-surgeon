# ADR 0003 — `patch_function` / `patch_struct` / `patch_interface` should accept line-number targets

## Status
Proposed — 2026-04-16. Not yet implemented.

## Context
Observed during the WriteFile refactor (ADR 0001), across 8+ patch
attempts in different files:

- **Multi-line text matching in `patch_function` fails consistently.** The
  tool description claims whitespace normalization, but any match that
  crosses a newline (e.g. an `if ... { ... }` block with embedded
  newlines) is rejected with "no match found."
- **The function signature line is unreachable by `patch_function`.** Only
  the body between braces is addressable. Changing a return type forces
  either `update` (whole-body rewrite, risks losing comments / ordering)
  or `Edit` (forfeits AST safety).
- **`update`'s "paste the whole body" contract is a regression** for
  small signature changes. It's the exact failure mode `patch_function`
  was meant to eliminate.

Meanwhile, `symbol body=true` already returns the body with **line
numbers**:

```
Code (Empty lines stripped):
313: if err := h.fs.WriteFile(ctx, action.FilePath, updatedSrc); err != nil {
314:     return nil, err
315: }
```

The agent already has the exact coordinates. Re-encoding those as a
text `match` is lossy — the tool rejects valid targets because of
whitespace drift between the display and internal storage.

## Decision
Extend `patchOpInput` (shared by `patch_function`, `patch_struct`,
`patch_interface`) with line-based targeting as an alternative to
`match` / `match_regex`:

- `at_line: N` — target a specific line number (1-based, relative to
  the symbol's body or the file for struct/interface declarations).
- `from_line: N, to_line: M` — target a line range (inclusive).
- `before_line: N` / `after_line: N` — for `insert_before` / `insert_after`
  ops, specify the anchor line directly.

These are mutually exclusive with `match` / `match_regex` /
`occurrence`. When used, go-surgeon skips fuzzy matching and operates
on the exact byte ranges computed from the AST + line numbering.

Line numbers map directly to what `symbol body=true` displays, so the
agent can round-trip: call `symbol`, see `L313: if err := ...`, pass
`at_line: 313` to `patch_function`, done.

## Alternatives considered

### A. Fix multi-line matching instead
Rejected as insufficient. Even if matching worked perfectly, the
signature line remains unreachable (`patch_function` only sees body
content between braces). And fuzzy matching is inherently less
reliable than coordinate-based targeting — whitespace drift, comment
differences, and occurrence disambiguation all vanish with line numbers.

### B. Expose a separate `patch_body_line` tool
Rejected. More tools to choose between means more prompt real estate
and more agent confusion. Integrating into existing `patch_*` tools
keeps the mental model simple: "same ops, different target style."

### C. Make `update` smarter about preserving structure
Rejected. `update` is correctly positioned as the "rewrite the whole
thing" tool. Making it infer which parts changed via diffing brings
back the fuzziness we're trying to eliminate.

## Open questions
- **Signature line numbering.** For `patch_function`, should `at_line: 1`
  mean the first line of the body (current behavior of `symbol body=true`)
  or the declaration line? Decision: keep the body-relative numbering
  from `symbol body=true`. Signature edits belong to a separate op
  (see ADR 0004, proposed).
- **Line stability across preview/apply.** If the agent patches lines
  100-105, the subsequent patch in the same request that targets line
  120 is now at line 119 (or 121, depending on direction). Decision:
  operate all line references against the **original** body, matching
  how text-match operations already work (resolve all edits first,
  then apply backwards). Document clearly.

## Consequences
**Positive**
- Eliminates the multi-line matching failure class from `.feedbacks/pass-1.md` §A.
- Removes the "round-trip a coordinate through text matching" lossy encoding.
- Makes disambiguation natural: line numbers are unique by construction,
  so `occurrence` becomes unnecessary with line-based ops.
- `symbol body=true` output becomes directly actionable.

**Negative**
- Tool description / schema grows. Needs clear doc on mutual-exclusion
  with text matching.
- Line numbers depend on what `symbol body=true` returns. If symbol-body
  formatting ever changes (e.g., "empty lines stripped" → not stripped),
  the agent's saved line number could drift between calls. Mitigation:
  document that line numbers must be used within the same conversation
  and against the same version of the file.

## Follow-ups
- ADR 0004 (proposed): add a `set_signature` op to `patch_function`
  / `patch_interface retype_method` for return-type-only changes that
  don't touch the body.
- Re-measure `.feedbacks` on the next benchmark pass to see how many
  Read/Edit uses this eliminates.
