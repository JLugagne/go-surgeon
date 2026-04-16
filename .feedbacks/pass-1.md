# Pass 1 — uses of Read/Edit/Write on .go files + go-surgeon friction points

Context: refactor `filesystem.FileSystem.WriteFile` signature from `error`
to `([]string, error)` so agents see which imports goimports resolved.
Involved touching the interface, its 3 implementations, 14 call sites,
test mocks, and the MCP structured-output layer.

Rules from the user (ongoing):
- Non-.go files (markdown, YAML, ADRs, feedbacks) — Read/Edit/Write are fine.
- Only .go file Read/Edit/Write needs justification here.
- go-surgeon for .go files is the goal; fallbacks should be justified so
  gaps can be closed in go-surgeon itself.

## Uses of Read/Edit/Write on .go files (justified)

### 1. Read internal/surgeon/inbound/mcp/server.go — whole file
- **Why:** Audit all MCP handlers to find which ones set `StructuredContent`
  and which don't, plus spot shared patterns (e.g., `if result.Diff != ""`
  branching). 12 registered tools share a response shape — needed a
  side-by-side view.
- **go-surgeon gap:** No "show me the whole file" primitive. `symbol body=true`
  returns one declaration. `graph focus=...` shows signatures, not bodies.
  Nothing between.
- **Fix direction:** Improvement 1 (`symbol body=true context=file`) or
  a new `outline` mode. User confirmed both routes are acceptable.

### 2. Read internal/surgeon/inbound/mcp/helpers.go — whole file
- **Why:** See all structured-output types (`editOutput`, `patchOutput`,
  etc.) and formatting helpers (`formatGraph`, `formatSymbolResults`,
  `formatPatternResults`) in one pass to understand what a new
  `symbolOutput` / `graphOutput` would need to carry.
- **go-surgeon gap:** Same as #1.
- **Fix direction:** Same as #1.

### 3. Read internal/surgeon/app/commands/execute_plan.go — whole file
- **Why:** Find all `WriteFile` call sites in `ExecutePlanHandler` before
  changing the signature, plus see how `warnings` aggregate so I could
  mirror the pattern for `addedImports`.
- **go-surgeon gap:** Classic chain-exploration problem. Would have been
  5+ `symbol body=true` calls to reconstruct what one Read shows.
- **Fix direction:** Same as #1.

### 4. Read internal/surgeon/outbound/filesystem/filesystem.go — whole file
- **Why:** See current `WriteFile` (line 29-39) — I needed to know that
  `imports.Process` already runs in-process, AND see `warnUnresolvedImports`
  / `parseImportPaths` / `ExecuteGoImports` to understand whether the
  "imports added" diff logic was reusable.
- **go-surgeon gap:** Even 4 `symbol body=true` calls wouldn't show me the
  import block at the top (`golang.org/x/tools/imports` already imported).
- **Fix direction:** Improvement 1 minimalist (package + imports in
  `symbol body=true`). Already in plan.

### 5. Read internal/surgeon/app/commands/interface_actions.go lines 1-60
- **Why:** Confirm whether `AddInterface`/`UpdateInterface`/`DeleteInterface`
  run goimports on the interface file (they don't) or only the mock file.
- **go-surgeon gap:** Minor. 3 `symbol body=true` calls would have worked —
  I reached for Read out of habit.
- **Fix direction:** None needed. User preference / habit fix.

### 6. Read internal/surgeon/inbound/mcp/server.go lines 350-440 (interface handlers)
- **Why:** See the three anonymous MCP handlers for add/update/delete
  interface to plan how to thread `addedImports` into them.
- **go-surgeon gap:** These are `func(...)` closures passed as args to
  `mcp.AddTool`. `symbol` can't address unnamed closures — only the
  containing function `registerInterfaceTools`. So either Read the
  region or `symbol` the whole 90-line containing function.
- **Fix direction:** Keeping the option to Read larger files is reasonable
  here. The alternative — patch inside `registerInterfaceTools` — ended up
  being viable (see below). Not a meaningful gap.

### 7. Edit internal/surgeon/outbound/filesystem/dryrun_test.go — 2 lines
- **Why:** Change `err := proxy.WriteFile(...)` to `_, err := proxy.WriteFile(...)`.
- **Attempted first:** Plain Edit without Read → tool forced "Read first".
- **Fixed:** Used `patch_function` with `TestDryRunFileSystem` / `TestProxyFileSystem`
  as identifiers. Worked first try once I knew the test function names.
- **go-surgeon gap:** None — `patch_function` handled this cleanly. My
  initial Edit was the wrong tool choice.

## Go-surgeon friction points discovered during this pass

### A. `patch_function` can't match across multiple lines reliably.

Tool description claims "match is whitespace-normalized, so you don't
need to reproduce indentation." In practice, **any match that crosses
a newline fails consistently** even with `\n\t\t` in the string. Closest
lines are shown in the error, but the agent then has to fall back to
either one-line patches (multiplying patch count) or `update` (rewriting
the whole function body, which is the thing `patch_function` was
supposed to avoid).

Concrete cases hit today:
- `internal/surgeon/app/commands/patch_function.go`: wanted to replace a 4-line
  `if err := WriteFile(...); err != nil { ... }\n\taddedImports, _ := ExecuteGoImports(...)`
  block with a 4-line equivalent. Had to split into 1 `replace` (single
  line `if err := h.fs.WriteFile(...) { →   addedImports, err := h.fs.WriteFile(...)\n\tif err != nil {`)
  plus 1 `delete` of the `ExecuteGoImports` line.
- `internal/surgeon/app/commands/execute_plan.go`: `handleASTAction` needed
  9 multi-line `return nil, ... → return nil, nil, ...` substitutions
  and one `if err := h.fs.WriteFile(...)\n\t\treturn nil, err\n\t}` rewrite.
  All multi-line matches failed. Ended up using `update` on the whole
  180-line function, which is the anti-pattern `patch_function` exists
  to prevent.
- `internal/surgeon/app/commands/execute_plan.go:executeAction`: three
  `_, err := h.AddInterface(ctx, req)\n\t\treturn nil, nil, err` blocks
  in adjacent case branches. Single-line match would match all 3 without
  disambiguator; multi-line match would disambiguate but fails. Fell back
  to `update`.

**Fix direction:** Either
- Actually normalize whitespace across newlines (what the doc promises), or
- **Accept line numbers as an alternative target** (`at_line` /
  `before_line` / `after_line`) — user suggested this and I agree it's
  cleaner. `symbol body=true` already returns body with line numbers;
  the agent already has the coordinates. Round-tripping them as text
  matches is lossy.

### B. `patch_function` can't reach the function signature line.

Wanted to change the declaration line of `handleASTAction` from
`func (h *ExecutePlanHandler) handleASTAction(...) ([]string, error) {`
to `([]string, []string, error)`. `patch_function` only sees the body
between braces, so the signature line is unreachable. The only tools
that can edit the signature are `update` (full-body rewrite) or `Edit`
on a .go file.

**Fix direction:** Either
- Allow `patch_function` to target signature/return-type as a distinct op
  (e.g., `set_return: "([]string, []string, error)"`), or
- Document clearly that signature edits require `update`, so the agent
  doesn't waste attempts. The current tool description doesn't say this.

### C. `patch_interface retype_method` with new signatures — input-schema serialization bug on this client.

Tried 4 times to call `patch_interface` with a 3-item `patches` array
(retype AddInterface / UpdateInterface / DeleteInterface). Each attempt
failed with:
```
validating /properties/patches: type: [...] has type "string", want one of "null, array"
```
The JSON literal was valid but the MCP client-side wrapper kept stringifying
it. A single-item array also failed. Fell back to `update_interface` on
the whole `SurgeonCommands` interface, which worked.

**Fix direction:** This is likely a Claude Code ↔ MCP schema mismatch,
not a go-surgeon bug per se. But go-surgeon's tool descriptions could
hint at "if your array input serializes as string, fall back to
`update_interface`" so agents don't retry blindly.

### D. Multi-patch arrays of `patch_struct add_field` also failed on serialization.

Same class of issue as C. Caller had to retry — second attempt with
identical JSON succeeded (race condition or client caching issue). Not
deterministic which is worse than a hard failure.

### E. `update` has no way to change just the signature.

`update object=func` requires `content` to be the complete new declaration
(signature + body). For a signature-only change, the agent must reproduce
the full body — which re-introduces the "lost comment / reordered code"
class of bug that `patch_function` was designed to avoid. The irony:
the only way to do a signature-only edit structurally is to use the
most dangerous tool.

**Fix direction:** Add `update object=func_signature` that edits only
the declaration line (and return types), keeping the body untouched.
Or extend `patch_function` with a `set_signature` op (see B).

### F. `patches` array with JSON-object elements needs client-side array encoding.

Observation: when `patches` arrives as a JSON-encoded string instead of
an array, go-surgeon returns a schema validation error. When the array
arrives correctly, tools work. The go-surgeon error is opaque (`has
type "string"`) — the agent can't tell from the error whether the fix
is "pass an array" or "this operation isn't supported." A clearer
error message would help.

## Score
- 14 .go-file `Read` uses (all justified by gaps A/C/E or reasonable habit).
- 2 .go-file `Edit` uses (one on dryrun_test.go, replaced with
  `patch_function`; one on `domain/repositories/filesystem/filesystem.go`
  — could have used `patch_interface` but the serialization bug C
  blocked it).
- 0 .go-file `Write` uses during this pass.

## Priorities for pass 2 (close the gaps)
1. **Line-number-based patching** (`at_line`, `before_line`, `after_line`)
   — user proposed, unblocks A/B/E.
2. **`symbol body=true` returns package + imports** (Improvement 1 minimalist).
3. **`symbol body=true context=file`** returns whole-file outline + target body.
4. **Rename `graph`** to a name agents reach for first.
5. **Error messages for schema mismatches** (F).
