# Blocker — go-surgeon MCP tools write to main, not the agent worktree

## Summary
The env has `/go/github.com/JLugagne/go-surgeon` symlinked to
`/home/jeremy/dev/Go/src/github.com/JLugagne/go-surgeon` (the main
branch), while this agent is running inside a worktree at
`.claude/worktrees/agent-a6c67564`. When go-surgeon MCP tools
(`create`, `patch_*`, `update`, `execute_plan`, ...) are invoked
with absolute paths under `/go/...`, they resolve to the main
branch working tree and write there — even though the agent's cwd
is inside the worktree.

This was observed during Sprint B #1:
- Initial `mcp__go-surgeon__create` / `patch_*` / `patch_interface`
  calls targeting `/go/github.com/JLugagne/go-surgeon/internal/...`
  modified the main worktree.
- `git status` from the main checkout showed unexpected modified
  files from those calls.
- The modifications had to be copied out, reverted in main, and
  re-applied manually to the worktree path.

## Impact
Sprint B #1 constraint says: "Use go-surgeon tools for all .go file
operations." That constraint is violated any time the agent needs
to edit the worktree: the tools quietly write to the wrong tree.

## Workaround used in this sprint
- Initial patch_decl.go creation and type additions were made via
  go-surgeon (writes landed on main).
- Files were copied into `/tmp/`, reverted in main via
  `git checkout`, then copied back into the worktree.
- All subsequent edits inside the worktree used Edit/Write tools.
  This is a deliberate exception to the "use go-surgeon for .go
  files" rule — the alternative was silent corruption of main.

## Fix candidates
1. The harness/MCP launcher should symlink `/go/.../go-surgeon` to
   the active worktree, not to main, when running inside one.
2. go-surgeon could refuse writes when the resolved real path is
   outside the cwd's git worktree (would error loudly instead of
   silently writing to main).
3. This agent should always call go-surgeon tools with
   worktree-absolute paths (`/home/jeremy/dev/Go/src/.../.claude/worktrees/...`)
   — verified; this does bypass the symlink. The issue is that the
   mcp__go-surgeon schema doesn't advertise which path shape is
   safe, and both "look" correct to an unfamiliar agent.
