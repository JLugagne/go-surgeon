# Pass 4 — .go Read/Edit/Write usage during the `graph`→`overview` rename

Task: rename the MCP `graph` tool to `overview` (ADR 0004) + update
all references in the MCP layer + tests + serverInstructions.

## Scoreboard

**go-surgeon operations:** 3 successful (2 patch_function + 1 update).
**.go Reads:** 2. Both justified.
**.go Edits:** 3. Two justified, one would have been cleanly handled
by a hypothetical future feature.
**.go Writes:** 0.

Total: **5 non-go-surgeon ops** where pass 2d+pass 3 had 0. Regression
caused by going after a target (package-level `const`) that exposes a
genuine gap.

## Details

### Read #1 — server.go:1-10 (package-level const location)
- **Why:** `serverInstructions` is a `const` declared at package scope.
  `symbol` only indexes funcs + types (not vars/consts), so I couldn't
  find its location or body through go-surgeon. Fallback: Read.
- **Gap:** `symbol` doesn't match vars/consts. Should match them.
  Fix: extend `FindSymbols` to also walk `*ast.GenDecl` with
  `token.VAR` / `token.CONST`.

### Read #2 — server.go:24-52 (serverInstructions body continuation)
- **Why:** Same const, second half. Same root cause.
- **Gap:** Same.

### Edit #1 — server.go: two lines inside `serverInstructions` const
- **Why:** Changing strings inside a top-level string const. `symbol
  body=true` can't see the const, `patch_function` is scoped to
  functions only, `update` on a const would replace the entire value
  (heavy). Fell back to Edit.
- **Gap:** No `patch_const` or `patch_var` tool. Low priority but
  real. Alternative: add a general `patch_text` that does text
  substitution inside a specified declaration after AST-locating it.

### Edit #2 — server_test.go: replace_all "graph" → "overview"
- **Why:** 14 occurrences of the string `"graph"` across 7 test
  functions. Could have done 7 `patch_function` calls; `Edit
  replace_all` was one call.
- **Gap:** Minor. `patch_function` doesn't support "apply this
  replacement across every matching function in a file." A
  `patch_file` text-substitution op (scoped to a file, not a symbol)
  would handle this cleanly. Lower priority than const/var support.

### Edit #3 — server_test.go: TestGraph_ → TestOverview_
- **Why:** Same class as #2 — rename across 6 function names.
  `rename_function` would need to be a new op.
- **Gap:** No batch-rename tool. For a codebase the size of
  go-surgeon, `Edit replace_all` on a restricted pattern is
  acceptable. For a big refactor (rename Repository → Store across a
  real project), a dedicated tool would matter more.

## Friction points discovered (new)

### P4-1. Package-level `const`/`var` declarations are opaque to `symbol`.
The index only knows funcs + types + methods. Agents can't locate
package-level vars/consts without Read/Grep fallback. Fix: extend the
AST walk in `FindSymbols` to include `ValueSpec` declarations.

### P4-2. No `patch_text_in_decl` or equivalent for non-function bodies.
For top-level strings (consts), structured-data vars, etc., the only
AST tool is `update` (whole-body rewrite). For long multi-line string
constants, text substitution is the natural operation. Similar to
what `patch_function` does inside function bodies.

### P4-3. `Edit replace_all` is the right tool sometimes.
Not every .go edit benefits from AST awareness. A codebase-wide
string rename (e.g., fixing a typo in a log message) is cleanly a
string substitution. Instead of building ever-more-specific AST
ops, consider: **accept that `Edit replace_all` with a user-confirmed
scope is a legitimate pattern for non-structural changes**. The agent
log can continue to track it for rare-use-case observability.

## Friction points CLOSED by pass 3 → confirmed
- **A1 (chained patch_struct anchors)** — worked fine in pass 4 (no
  struct patches in this task).
- **A2 (gofmt-before-diff)** — worked fine.

## Friction points still open after pass 4
- P4-1: const/var indexing. High leverage.
- P4-2: text-patch-in-decl. Medium leverage.
- P4-3: accept `Edit` as legitimate for non-structural changes. Low
  leverage — mostly a documentation / tool-description change.

## Bottom line

Pass 4 surfaced the real limit of AST-only tooling: **package-level
string constants are the escape hatch case**. Agents will always need
a text-level fallback for them unless go-surgeon grows const/var
support. The Pass 1→Pass 3 score (14+2 fallbacks → 0) was enabled by
narrow, high-leverage additions (symbol imports, context=file,
line-based patching). Pass 4's 2 Reads + 3 Edits is not a regression
in those features — it's a surface exposure in a different
dimension.

Recommended Pass 5 targets (prioritized):
1. Extend `symbol` to index `const` and `var` declarations.
2. Extend CLI `graph` → tag as alias, plan formal rename next major.
3. Consider `patch_const` / `patch_var` surgical ops if usage justifies.
