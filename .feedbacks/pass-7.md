# Pass 7 — Friction Report (next-iteration implementation)

## Session context
Implementing 5 improvements from the go-surgeon iteration plan: full body in patch errors, overview/symbol description reframing, description density audit, whitespace-flexible token matching.

## Friction: Edit fallback on string-literal edits in server.go

**What happened.** When trimming all 18 tool descriptions (Task #4), the agent (me) used `Edit` instead of `patch_function` for ~15 string literal replacements inside `registerQueryTools`, `registerActionTools`, `registerInterfaceTools`, `registerInsertCallTool`, `registerOtherTools`, `registerPatchTools`, `registerPatchStructTool`, `registerPatchInterfaceTool`.

**Why.** Three triggers:
1. The first attempt to edit `serverInstructions` with `patch_function` correctly failed — it's a `const`, not a function. This primed the switch to `Edit`.
2. The description strings are long single-line Go string literals. `patch_function` match would need to reproduce a substantial portion of the string verbatim, which felt fragile.
3. `Edit` with `old_string`/`new_string` was a natural fit for "find this unique string, replace with that" on string literals.

**Was the fallback justified?**
- For `serverInstructions` (const): yes — `patch_function` can't edit consts. `update` would have been the correct go-surgeon tool.
- For descriptions inside register functions: no — `patch_function` with `match` on the unique description text would have worked. The match strings would be long but unique within the function body.

**Implication.** The agent's mental model of "when does patch_function work" is imprecise around string literals inside function arguments. The description says "edit a few lines inside ONE function body by matching on text" which technically covers this case, but the agent doesn't naturally think of modifying a string literal argument as "editing a function body."

**Possible mitigation.** None needed in go-surgeon itself — this is an agent behavior issue, not a tool gap. The correct tools exist (`patch_function` for string args, `update` for consts). The agent just needs to stay disciplined.

## Friction: patch_function multi-line match failure

**What happened.** The first `patch_function` call on `PatchFunction` itself (to modify the error return block) used a multi-line `match` string and failed with "no match found."

**Recovery.** Switched to line-based targeting (`from_line`/`to_line`) which worked immediately. This is the correct recovery path — line-based targeting is unambiguous after `symbol body=true`.

**Root cause.** The match string had literal `\n` and `\t` in the JSON, but the actual body's indentation or line structure differed slightly. The whitespace normalization (step 3) should have caught this, but it didn't — suggesting the match string had a content difference, not just whitespace.

**Implication.** Line-based targeting is strictly better for edits where you've already read the body. The new step-4 token matching fallback (added in this session) would also have caught this case.

## Friction: parallel Edit on same file

**What happened.** For the description audit, the agent sent Edit calls sequentially (one per tool description) rather than in parallel. Each Edit modifies the file, so parallel calls could conflict.

**Implication.** This is correct behavior — sequential edits on the same file are necessary. But it meant 15+ sequential tool calls for what was conceptually one task. `execute_plan` with multiple `update_func` actions could have batched these, but it doesn't support editing string constants inside function bodies.

## No friction

- `create` for adding new functions (formatNumberedSource, token matching) worked smoothly.
- `patch_function` with line-based targeting worked perfectly for all three PATCH_FAILED error path changes.
- `symbol body=true` + `context=file` provided all needed context in one call.
- `overview symbols=true` gave a good initial map of the codebase.
- goimports correctly added `go/scanner` when the token matching code was created.
