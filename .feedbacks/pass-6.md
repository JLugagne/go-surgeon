# Pass 6 — dog-food after const/var indexing landed

Task: extract duplicated `"field %q not found"` formatting from
`applyStructPatch` (5 branches) into a single helper `fieldNotFoundMsg`.

## Scoreboard
- **go-surgeon operations:** 6 (1 `create` + 5 `patch_function` replaces).
- **.go Reads:** 0.
- **.go Edits:** 0.
- **.go Writes:** 0.

Clean pass — every operation went through go-surgeon primitives.

## Observations

### Positive — const/var indexing worked end-to-end
During the task I reached for `symbol query=<const_name>` once to
verify a constant's location. In pass 4 this was a Read fallback.
Now it returns the decl cleanly.

### Minor friction — `occurrence` counting needs care for variant matches

The function had 5 sites to change across 4 case branches:
- 4 variants used `p.Name` (remove/retype/set_tag/set_doc)
- 1 variant used `p.From` (rename)

I asked for 3 × occurrence replacements on the `p.Name` string, then
1 on `p.From`. Got 4 patches applied on the first call. But the
`p.Name` occurrences actually counted 3 — remove, retype, set_tag —
leaving `set_doc` (the 4th `p.Name` variant) untouched. I had to
issue one more `patch_function` call to clean it up.

Root cause: when matching `p.Name`, the tool counts occurrences in
the order they appear in the body. If I had pre-read that order
(which I could via `symbol body=true` with line numbers) I would have
asked for occurrence: 4 as well. Minor UX — if the agent asks for N
occurrences and there are N+1, they should be warned about the extra.

**Suggested fix:** when a patch_function replace uses `occurrence: N`
and the body still contains additional matches of the same text, the
response could include a note: "2 more matches not replaced at lines
X, Y — re-run if you meant all of them." Low priority.

### The line-based targeting path didn't come up this round
Because the changes were cleanly matched by single-line text patterns,
`at_line` wasn't needed. The presence of the line-based mode didn't
hurt — I didn't accidentally reach for it. Naming feels right
("if text matching is ambiguous, use at_line").

## Remaining friction points from pass-4 still open
- **P4-2** (patch_text in non-function decls): still not addressed.
  Didn't hit it this pass.
- **P4-3** (Edit replace_all as legitimate tool for batch renames):
  philosophical, no change.

## Status
Pass 1→6 scoreboard trend:
- Pass 1: 14 Reads + 2 Edits (pre-improvements)
- Pass 2 (after 2a/2b/2c): 0 of either × 4 tasks
- Pass 3 (struct chain + gofmt-diff): 0 of either
- Pass 4 (graph rename): 2 Reads + 3 Edits (const/var gap)
- Pass 5 (const/var indexing): closed P4-1
- Pass 6 (dog-food): 0 of either

Recent passes land at 0/0 as expected unless the task hits a gap
that hasn't been closed yet. Open gaps (P4-2, P4-3) are narrow —
string-inside-a-const edits and codebase-wide text substitutions.
For the rest of real Go refactoring work, go-surgeon primitives cover
the ground.
