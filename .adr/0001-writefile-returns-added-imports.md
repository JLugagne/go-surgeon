# ADR 0001 — `WriteFile` returns the list of imports added by goimports

## Status
Accepted — 2026-04-16.

## Context
The `filesystem.FileSystem` interface had two separate entry points for
import management:

- `WriteFile(ctx, path, content) error` — on `.go` paths, silently ran
  `golang.org/x/tools/imports.Process` in-process as part of the write.
- `ExecuteGoImports(ctx, files) ([]string, error)` — shelled out to the
  `goimports` binary and returned the list of imports added (diff of the
  import block before and after).

Several command handlers (`patch_function`, `patch_struct`,
`patch_interface`, `tag_struct`, `test_gen`, `interface_actions`)
called BOTH on the same file on every write:

```go
h.fs.WriteFile(ctx, path, src)          // imports pass #1 (in-process)
h.fs.ExecuteGoImports(ctx, []string{p}) // imports pass #2 (shell-out)
```

Problems with that shape:

1. Every edit ran goimports twice, once in-process and once via
   `exec.CommandContext("goimports", ...)`. The shell-out typically
   costs 100-300ms per call, compounding across plans.
2. The `ExecutePlanHandler` write path (create/update/delete/insert_call
   /execute_plan/add_interface/update_interface/delete_interface for
   the interface file) called `WriteFile` but **not** `ExecuteGoImports`,
   so those tools' results never surfaced `AddedImports` to the agent —
   while patch tools did. Inconsistent output for the same underlying
   operation.
3. The "net-new imports" diff logic only lived in `ExecuteGoImports`,
   meaning the in-process `WriteFile` had the information (it knew the
   before/after import sets) but threw it away.

## Decision
Move added-imports tracking into `WriteFile` itself, return it from
every write, and delete `ExecuteGoImports` from the interface.

New signature:

```go
WriteFile(ctx context.Context, path string, data []byte) (addedImports []string, err error)
```

All call sites now capture the return. `ExecutePlanHandler.executeAction`
aggregates added imports across all actions and returns them via
`PlanResult.AddedImports`. `PatchFunctionResult`, `PatchStructResult`,
`PatchInterfaceResult` fill their existing `AddedImports` field from
the `WriteFile` return instead of the deleted `ExecuteGoImports` call.

## Alternatives considered

### A. Status quo — two entry points, redundant shell-out
Rejected. See "Problems" above. Double-pass cost and inconsistent
output across tools.

### B. Keep `ExecuteGoImports`, add `AddedImports` return to `WriteFile`
Rejected. Two code paths that compute the same diff is strictly worse
than one. The only reason to keep `ExecuteGoImports` would be for
multi-file batch import resolution (e.g., "run goimports on these 10
files in one shell invocation"), but no caller ever used it that way —
every call site passed a single file.

### C. Narrow refactor — add goimports call to `ExecutePlanHandler` only
Rejected. Would have left the double-pass inefficiency and kept two
ways to get the same information. A wider refactor now avoids a
later one.

## Consequences

**Positive**
- Single source of truth for "imports added during this write."
- Every edit tool can surface `AddedImports` uniformly.
- ~100-300ms saved per edit that previously shelled out.
- One fewer method on the `FileSystem` interface.

**Negative**
- Breaking signature change: `WriteFile` now returns `([]string, error)`.
  Every implementation (real FS, dry-run, proxy, test mocks) and every
  caller (14 call sites) must change in the same commit. This is a
  one-time cost.

## Follow-ups
- `editOutput` (MCP structured output for create/update/delete/insert_call/
  execute_plan/interface tools) will gain an `AddedImports` field so
  agents see them uniformly.
- `patchOutput` will expose `AddedImports` as a structured field (the
  earlier commit surfaced it only in the text prefix, which was a miss).
