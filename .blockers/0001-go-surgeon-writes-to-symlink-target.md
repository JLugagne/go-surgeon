# Blocker 0001 — go-surgeon MCP tools write to the symlink target, not the caller's worktree

## Context
The env has `/go/github.com/JLugagne/go-surgeon` symlinked to
`/home/jeremy/dev/Go/src/github.com/JLugagne/go-surgeon` (the main
branch). Sprint B launched 5 parallel subagents in isolated worktrees
under `.claude/worktrees/agent-*`. When a subagent calls a go-surgeon
MCP tool with a **relative path** or an absolute path under `/go/...`,
the tool resolves to the symlink target (main) and writes there — even
though the subagent's cwd is inside its worktree.

Observed during Sprint B #1 (patch_decl) subagent. Files had to be
copied out of main, reverted, and re-applied by hand to the worktree.
The subagent used `Edit`/`Write` (not go-surgeon) for the remainder of
its work to avoid silent corruption of main.

## Blocker
What's the right fix to make go-surgeon safe under worktrees?

## Options
- [ ] **A. Fix the harness: symlink `/go/…/go-surgeon` to the active
  worktree when one is in use.** Pros: transparent to every agent, no
  MCP behavior change. Cons: requires harness/launcher coordination
  outside this repo; only helps the Claude Code wrapper, not other
  clients.
- [ ] **B. Add a guard in go-surgeon: refuse writes when the resolved
  real path escapes the current working-directory git worktree.**
  Pros: loud error beats silent corruption; product-local fix; helps
  every MCP client. Cons: extra per-write check; edge cases around
  paths in symlinked module caches need whitelisting.
- [ ] **C. Document the requirement and rely on agents passing
  worktree-absolute paths.** Pros: zero code change. Cons: silent
  failure mode persists; unfamiliar agents will keep tripping.
- [ ] **D. Combine A + B** — harness fix is the ergonomic path;
  go-surgeon guard is the safety net when a client forgets.

## Default if unanswered
**D** — I'll open a follow-up task to add the go-surgeon guard (B) in
the next Sprint. The harness symlink fix (A) I'll flag for separate
discussion since it lives outside this repo.

## Scope impact
This blocker did NOT prevent Sprint B from shipping — all 5 subagents
completed and their work is merged on main (commits 424f4de → d9f4ab5).
The blocker matters for future multi-subagent sessions to avoid the
same clean-up cost.
