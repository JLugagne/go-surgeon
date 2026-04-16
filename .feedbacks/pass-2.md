# Pass 2 — .go Read/Edit/Write usage after Pass 2a/2b/2c shipped

Context: after landing
- Pass 2a: `symbol body=true` returns package + imports
- Pass 2b: `symbol body=true context=file` returns sibling outline
- Pass 2c: line-based targeting (`at_line`, `from_line`, `to_line`) on `patch_function`

I ran two dog-food tasks inside the go-surgeon repo itself:

## Task 1 — Pass 2a implementation (earlier in the session)
Added `Package` + `Imports` fields to `SymbolResult`, populated them in
`extractFuncResult` / `extractStructResult`, updated the MCP formatter.

**Score:** 0 .go Reads, 0 .go Edits, 0 .go Writes. 
All operations went through `patch_struct`, `update`, `create`,
`patch_function`. No fallbacks.

## Task 2 — Pass 2b implementation
Added `Context` / `FileOutline` / `OutlineEntry`, created
`buildFileOutline`, plumbed through `FindSymbols` / MCP input /
formatter.

**Score:** 0 .go Reads, 0 .go Edits, 0 .go Writes.
All operations structural. Encountered two annoyances:
- `patch_struct add_field` with `after=` anchor failed when chaining
  three fields in one call (second patch can't anchor on a field that
  the first patch just added). Worked around by issuing three separate
  calls. Worth fixing — should be atomic.
- `insert_after` in `formatSymbolResults` accidentally nested the
  outline block inside `if len(res.Imports) > 0`, so files with no
  imports wouldn't show the outline. Caught by re-reading the function
  body via `symbol body=true`. Not a go-surgeon bug — I picked the
  wrong anchor line. But a preview-mode warning for brace-level changes
  would have caught it.

## Task 3 — Pass 2c implementation
Added `AtLine` / `FromLine` / `ToLine` to `FunctionPatch` + `patchOpInput`,
wrote `resolveBodyLineRange` / `buildLineModeEdit`, integrated into
`PatchFunction`.

**Score:** 0 .go Reads, 0 .go Edits, 0 .go Writes.
One interesting moment: **I hit friction A from pass-1** while patching
`PatchFunction` itself (multi-line match failed). I worked around by
using `insert_after` with a single-line anchor plus a second `replace`
on a single-line variant. This is the exact class of failure that line
mode now fixes — but `PatchFunction` is the tool implementing line
mode, so I couldn't use line mode to fix it. A textbook bootstrap
problem. Non-blocking.

## Task 4 — Pass 2d dog-food (this task)
Extracted the duplicated goimports-diff logic from
`FileSystem.WriteFile` and `DryRunFileSystem.WriteFile` into a shared
`applyGoImports` helper.

**Score:** 0 .go Reads, 0 .go Edits, 0 .go Writes.
3 operations (1 `create` + 2 `update`). All first-try success. The
refactor was ~40 lines of code changes and required zero file reads
because `symbol body=true` already gave me the two existing WriteFile
bodies with package + imports + outline (when I asked for
`context=file`, which I reached for naturally on this task).

---

## Friction points still open after pass 2

### A1. `patch_struct add_field` with chained anchors.
Several patches in a single call can't reference fields that earlier
patches in the same call added. Example: adding `AtLine`, `FromLine`,
`ToLine` fields with `after=previous-field-just-added` anchors fails on
the 2nd/3rd patch. Workaround: call `patch_struct` three times. Fix:
resolve patches against the *evolving* struct within a single call
(anchors should see prior adds).

### A2. `patch_struct add_field` formatting is slightly off.
After adding fields via `patch_struct`, gofmt-style alignment of the
struct's field-name/type columns is lost:
```go
    Wrap       string `json:"..."`
    AtLine int `json:"at_line,..."`   // not aligned with the other fields
```
Cosmetic, but noticeable in diffs. `gofmt` (via goimports) should fix
it on write, but the MCP diff shown to the user doesn't include that
re-formatting pass. Either run gofmt on the struct before computing
the diff, or at least note it.

### A3. Line-based targeting for `patch_struct` / `patch_interface`.
Still text/AST only. Struct fields and interface methods are already
AST-addressable by name, so line mode is less urgent there — but for
consistency and to cover edge cases (e.g., target a specific field
when two fields share a name via embed shadowing), line mode would
help. Low priority.

### A4. Previewing a patch via `preview=true` returns only the diff,
not the full resulting body. For sanity-checking a line-mode change I
occasionally want to see the result in context. Could add
`preview_context=N` (N lines above/below) to the output. Low priority.

### A5. `insert_after` can land inside the wrong control-flow block
when the anchor line appears at the end of an inner block. In Task 2
I anchored on `fmt.Fprintf(&sb, "  %s\\n", imp)` inside a `for imp :=
range res.Imports` loop, and the inserted code landed inside the loop
instead of after it. Not a bug — the anchor match is literal — but a
preview-mode warning "your insert lands inside an inner scope, continue?"
would save a debug cycle.

## Friction points CLOSED by pass 2

- **#1/#2/#3 from pass-1 (whole-file Reads for cross-decl context):**
  `symbol body=true context=file` covers this. Didn't reach for Read
  once during Pass 2b/2c/2d.
- **#4 from pass-1 (Read for imports):** `symbol body=true` now
  includes them. Zero Reads for imports.
- **A from pass-1 (multi-line match fails in patch_function):** replaced
  by line-based targeting. The one residual hit was during
  self-implementation (bootstrap), not user-facing.
- **B from pass-1 (signature line unreachable):** line mode with
  `at_line=1` can now target the first body line; the signature itself
  still needs `update` but is documented. Partial fix.

## Bottom line

The Read/Edit-on-.go scoreboard went from **14 Reads + 2 Edits** in
pass 1 to **0 of either** across four Pass 2 tasks. Primary drivers:
- `symbol body=true` now carries ambient context (imports, package,
  sometimes outline).
- Line-based targeting means multi-line edits no longer fall back to
  `update` or `Edit`.

The remaining friction is incremental polish (A1-A5), not a blocker.
