# Open follow-ups

Frictions identified while dog-fooding go-surgeon through a ~2-day
improvement sprint (April 2026). Ranked by impact × 1/complexity.
Priorities: **P0** = ship next, **P1** = queue for a focused sprint,
**P2** = opportunistic.

| #  | Task                                              | Impact | Cx | Priority |
|----|---------------------------------------------------|:------:|:--:|:--------:|
| 23 | `patch_*_bulk` — multi-target variants            |   5    | 3  | **P0**   |
| 24 | Louder auto-lift notice in text output            |   3    | 1  | **P0**   |
| 25 | `rename_symbol` export-change escape hatch        |   3    | 1  | **P0**   |
| 26 | Top-level-only match scoping in `patch_function`  |   4    | 2  | **P0**   |
| 27 | `patch_file scope=code_only`                      |   4    | 3  | **P1**   |
| 28 | Edit body of a named nested closure / func lit    |   4    | 3  | **P1**   |
| 29 | `create object=file` preview                      |   2    | 1  | **P1**   |
| 30 | `describe_tool` JSON output                       |   2    | 1  | **P1**   |
| 31 | Per-op tool help in `describe_tool`               |   2    | 2  | **P2**   |
| 32 | `symbol pattern + outline` mode                   |   2    | 2  | **P2**   |
| 33 | Ship the `Edit/Write on *.go` PreToolUse hook     |   5    | 2  | **P0**   |

Task #33 is the one that would *close* `.issues/0001` and
`.issues/0002` — a harness-level enforcement of the "no generic
Edit/Write on `.go` files" rule. Listed last because it's a meta
task, but it's actually the highest-leverage unshipped item.

---

## 23 — `patch_*_bulk` multi-target variants  **P0** (impact 5, complexity 3)

**Problem.** Adding `Preview bool` to 15 input structs in `server.go`
is 15 separate `patch_struct` calls. Subagents fall back to `Edit`
when the ceremony gets heavy (`.issues/0002`). Same friction hits
`patch_function` when the same signature change applies across many
funcs in a file.

**Proposal.**
- `patch_struct_bulk { items: [{file, identifier, patches}] }`
- `patch_function_bulk { items: [{file, identifier, patches}] }`
- Share the existing per-tool handlers: the bulk tool is a loop
  over items that calls the existing `PatchStruct` / `PatchFunction`
  methods, accumulating file diffs, rolling back on any failure.
- Count against a soft cap (e.g. 20 items per call) so agents don't
  misuse it as "apply a giant refactor in one shot."

**Acceptance.** Replacing `patch_struct` × 15 in server.go with a
single `patch_struct_bulk` call produces an identical diff. One
rollback test: second item fails → first item's write is unwound.

---

## 24 — Louder auto-lift notice in text output  **P0** (impact 3, complexity 1)

**Problem.** `auto_lifted: true` is set in StructuredContent, but the
text response leads with "insert applied" followed by the lift info.
I almost missed it during wave 1. Agents that only read the text
response won't catch the correction.

**Proposal.** When `auto_lifted: true`, prepend the text with:
```
⚠ AUTO-LIFTED: anchor moved from <from> to <to>
```
Then the existing body. The emoji is optional but visual distinction
matters.

**Acceptance.** A test asserts the text starts with `⚠ AUTO-LIFTED`
(or equivalent loud marker) whenever `AutoLifts` is non-empty.

---

## 25 — `rename_symbol` export-change escape hatch  **P0** (impact 3, complexity 1)

**Problem.** `rename_symbol` refuses case-flip renames
(`computeAffectedPackages` → `ComputeAffectedPackages`) by design.
It's correct for 95% of cases but has no escape hatch, so a subagent
had to work around it with `update` + `patch_function`.

**Proposal.** Add `allow_export_change: bool` (default false). When
set, the rename proceeds and the response includes a `warnings: []`
entry naming the flipped export status. Text output calls it out
prominently.

**Acceptance.** Test: rename with `allow_export_change: false` keeps
current refusal. Rename with `allow_export_change: true` succeeds and
emits the warning.

---

## 26 — Top-level-only match scoping in `patch_function`  **P0** (impact 4, complexity 2)

**Problem.** `patch_function match="registerPatchTools(s, commands)"`
inside a function that contains closures finds the match in *both*
the outer body and any closure bodies. The `match N times` error
fires even though the agent obviously wants the top-level hit.

**Proposal.** Default `patch_function` to match only at the target
function's top-level block (i.e. direct statements in `fn.Body.List`
and their non-nested descendants, up to but not including any
`*ast.FuncLit`). Add `include_nested: bool` (default false) for the
rare case where an agent wants a closure-body hit.

**Related to** #28 — that adds the positive case (edit a specific
closure by name). This adds the negative case (ignore closures by
default).

**Acceptance.** Test: `patch_function` on a function with 3 closures
containing the same match string now touches only the outer body.
Test: `include_nested: true` restores current behavior.

---

## 27 — `patch_file scope=code_only`  **P1** (impact 4, complexity 3)

**Problem.** `patch_file match="oldName"` replaces in comments and
string literals too. The re-parse catches syntax breakage but not
semantic drift (e.g. a `// TODO: oldName` comment gets rewritten
silently). Agents can't use `patch_file` safely for anything that
might appear in prose.

**Proposal.** Add `scope: string` param with values:
- `all` (default, current behavior)
- `code_only` — skip positions whose AST node chain includes
  `*ast.Comment` or is inside a string literal's token range
- `identifiers_only` — match only at positions where the token is an
  `*ast.Ident`; most precise but requires the match string to look
  like an identifier

Implementation: during the re-parse, walk comments and string lits;
build a position-exclusion set; apply matches only where the
position isn't in the exclusion set.

**Acceptance.** Test: file with `// foo` comment and `"foo"` string;
`patch_file match="foo" replace="bar" scope=code_only` rewrites
identifiers but leaves comment and string untouched.

---

## 28 — Edit body of a named nested closure / func lit  **P1** (impact 4, complexity 3)

**Problem.** `patch_function identifier=NewServer` scopes correctly
to a top-level function. But when an agent needs to edit the body of
a specific closure inside that function (e.g. a `func(req, in) {...}`
passed to `mcp.AddTool`), there's no named handle. Only `at_line`
works, which requires a line lookup first.

**Proposal.** Extend the identifier syntax to support nested paths:
- `NewServer>closure[2]` — the 3rd `*ast.FuncLit` inside `NewServer`
  (positional, ordered by appearance)
- `NewServer>AddTool[find_definition]` — the `*ast.FuncLit` passed to
  `AddTool` where the tool name is `find_definition` (content-based)

Start with positional (`closure[N]`), add content-based later.

**Acceptance.** Test: `patch_function identifier="NewServer>closure[0]"`
edits only the first closure body, leaving outer body and sibling
closures untouched.

---

## 29 — `create object=file` preview  **P1** (impact 2, complexity 1)

**Problem.** Universal preview (#7, shipped) covers the write tools,
but `create object=file` still writes immediately. Inconsistent.

**Proposal.** Honor `preview=true` on `create object=file`: return
the would-be file contents as a "new file" diff without writing.

**Acceptance.** Test: `create object=file file=new.go preview=true`
returns a diff, `os.Stat(new.go)` returns `ErrNotExist`.

---

## 30 — `describe_tool` JSON output  **P1** (impact 2, complexity 1)

**Problem.** `describe_tool` returns a human-readable catalog. For
docs tooling / CI checks that want to diff the catalog against
registered tools, there's no machine-readable form.

**Proposal.** Add `format: "text" | "json"` (default `text`). With
`format=json`, return the catalog in StructuredContent as
`{tools: [{name, category, summary, example, related}]}`.

**Acceptance.** Test: `describe_tool format=json` returns
StructuredContent with all 27 entries; each entry round-trips through
`json.Marshal` cleanly.

---

## 31 — Per-op tool help in `describe_tool`  **P2** (impact 2, complexity 2)

**Problem.** `describe_tool name=patch_function` shows tool-level
summary, but the `patch_function` schema has 5+ ops (`replace`,
`insert_before`, `insert_after`, `delete`, `wrap`, `set_signature`)
and several of them aren't self-explanatory. An agent has to read
the JSON schema to learn that `set_signature` exists.

**Proposal.** Add `name=patch_function.set_signature` syntax: returns
the op's required fields, a 1-line description, and a canonical
example. Extend the `toolCatalog` entries to carry an `ops` map for
tools that have multi-op schemas.

**Acceptance.** Test: `describe_tool name=patch_function.set_signature`
returns a non-empty description mentioning `params` and `returns`.
Test: `describe_tool name=patch_function.nonsense` errors clearly.

---

## 32 — `symbol pattern + outline` mode  **P2** (impact 2, complexity 2)

**Problem.** `symbol pattern="^register" body=false` returns
signatures grouped by kind. It's an index, not an explorer. When
investigating a family of similar functions, the agent often wants a
one-line "what does each do" view (first sentence of doc comment).
Current options: signature only (too little) or body (too much).

**Proposal.** Add `outline: true` to pattern mode: returns, per
match, signature + first sentence of doc comment (stops at first `.`
or newline).

**Acceptance.** Test: pattern match against a file with doc-commented
funcs returns outline entries with the first-sentence summary.

---

## 33 — PreToolUse hook: reject `Edit`/`Write` on `*.go`  **P0** (impact 5, complexity 2)

**Problem.** `.issues/0001` and `.issues/0002` both document the same
rule: don't use generic `Edit`/`Write` on `.go` files. The rule is
policy, not enforcement — both regressions happened under volume /
time pressure despite explicit prompts.

**Proposal.** Mirror the `#15` bash-ls hook pattern
(`.claude/hooks/nudge-overview.sh`) but as a **blocking**
`PreToolUse` hook:

```bash
# .claude/hooks/block-go-edit.sh
# Rejects Edit/Write on *.go with exit 2 + stderr message pointing
# to the right go-surgeon tool based on the intended operation.
```

Hook logic:
- Match `Edit` or `Write` tool calls.
- Extract `file_path` from `tool_input`.
- If it ends in `.go`, exit 2 with a stderr message:
  ```
  blocked: generic Edit/Write on .go files violates project policy.
  Use mcp__go-surgeon__patch_function / patch_struct / patch_interface
  / patch_file / patch_decl / create / update instead. See
  AI_INSTRUCTIONS.md for the mapping.
  ```
- Otherwise exit 0.

**Acceptance.**
- Hook installed in `.claude/hooks/block-go-edit.sh` + wired in
  `.claude/settings.json` under `PreToolUse`.
- Manual test: `Edit` on a `.go` file gets rejected with the message
  above; `Edit` on a `.md` file proceeds.
- On rejection, `.issues/0001` and `.issues/0002` can be moved to
  `.issues/solved/`.

---

## Tracking

Move each item to `.issues/solved/` after shipping, mirror in
commits with the task number (`feat(#23): patch_struct_bulk …`).
If a P0 item still open after the next sprint, re-evaluate priority
— chronic P0s usually mean the problem is harder than estimated.
