package discovery

import (
	"fmt"
	"sort"
	"strings"
)

// RenderSkill returns the contents of a Claude SKILL.md describing how
// to use go-surgeon. The output is suitable for writing to
// `.claude/skills/go-surgeon/SKILL.md`. An agent reading this file
// learns when to reach for go-surgeon and where to find detail (the
// CLI itself, via `go-surgeon discovery`).
func RenderSkill() string {
	var sb strings.Builder
	sb.WriteString(skillFrontmatter)
	sb.WriteString(skillBody)
	sb.WriteString("\n## Tool catalog\n\n")
	sb.WriteString("Run `go-surgeon discovery` for the live grouped list, or `go-surgeon discovery <tool>` for per-tool detail.\n\n")
	sb.WriteString("Categories:\n\n")

	grouped := map[string][]ToolEntry{}
	for _, e := range Catalog {
		if strings.Contains(e.Name, ".") {
			continue
		}
		grouped[e.Category] = append(grouped[e.Category], e)
	}
	for k := range grouped {
		rows := grouped[k]
		sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
		grouped[k] = rows
	}
	for _, cat := range CategoryOrder {
		rows := grouped[cat]
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "### %s\n\n", CategoryLabels[cat])
		for _, r := range rows {
			fmt.Fprintf(&sb, "- **%s** — %s\n", r.Name, r.Summary)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

const skillFrontmatter = `---
name: go-surgeon
description: AST-aware editor for Go files. Use go-surgeon (CLI or MCP tools) instead of Read/Edit/Write/Grep/Glob/Bash for anything that touches a .go file — including reading Go code. Triggers on any Go editing or exploration task: refactoring, renaming, adding/removing functions/structs/interfaces, generating mocks, running ` + "`go build`" + ` or ` + "`go test`" + `, exploring an unfamiliar Go codebase. Do not start with go-surgeon and then fall back to Grep mid-task.
---

`

const skillBody = `# go-surgeon

go-surgeon edits Go source through the AST: every change is parsed, type-checked when relevant, and re-emitted via gofmt+goimports. It is faster and safer than text-level tools because:

- it preserves comments, doc strings, struct tag layout, and field/method order you didn't touch
- it manages imports for you (never hand-edit ` + "`import (...)`" + `)
- it ships type-aware refs (find_definition, find_references, rename_symbol) that work cross-package

## Mental model: EXPLORE → EDIT → VALIDATE

**EXPLORE** before you edit, to understand what's there.

- ` + "`overview`" + `: list packages and symbols across the project. START HERE on an unfamiliar codebase. Pass ` + "`focus=pkg/path symbols=true`" + ` to see every type/func/interface in one call.
- ` + "`symbol`" + `: read one declaration. ` + "`query='Name'`" + ` for a known symbol; ` + "`pattern='regex'`" + ` to list matches. Set ` + "`body=true`" + ` before any edit and ` + "`context=file`" + ` to see siblings in the same call.
- Both accept ` + "`module='github.com/org/repo'`" + ` to read inside a dependency.

**EDIT** — pick the narrowest tool that fits. Bigger tools aren't safer; they rewrite more.

| Goal                                              | Tool |
|---------------------------------------------------|------|
| Lines inside one function body                    | ` + "`patch target=function`" + ` |
| Multi-line replacement / restructure a func       | ` + "`update object=func`" + ` (op=replace is fragile across line boundaries) |
| Same rename across many funcs in one file         | ` + "`patch target=file`" + ` |
| Edit the value of a top-level const/var           | ` + "`patch target=decl`" + ` |
| Single field change on a struct                   | ` + "`patch target=struct`" + ` |
| Single method change on an interface              | ` + "`patch target=interface`" + ` (regenerates the mock) |
| Insert one statement at a fixed position          | ` + "`insert_call`" + ` |
| Whole-declaration replacement (func/struct/file)  | ` + "`update`" + ` |
| Brand-new declaration                             | ` + "`create`" + ` |
| Remove a declaration                              | ` + "`delete`" + ` |
| Add an interface WITH its mock atomically         | ` + "`interface action=add`" + ` (set ` + "`mock_file`" + ` + ` + "`mock_name`" + `) |
| 3+ coordinated edits that must land together      | ` + "`execute_plan`" + ` (atomic, up to 15 actions) |

**VALIDATE** after editing.

- ` + "`build_check`" + `: ` + "`go build`" + ` with structured diagnostics; pass ` + "`affected_by=path/to/file.go`" + ` to scope to its reverse-dep closure.
- ` + "`test_run`" + `: ` + "`go test`" + ` with a compact pass/fail report.

## Universal rules

- ` + "`content`" + ` is raw Go: never include ` + "`package ...`" + ` or ` + "`import (...)`" + ` blocks. ` + "`goimports`" + ` runs after every edit.
- Always read with ` + "`symbol body=true`" + ` before ` + "`update`" + `/` + "`delete`" + `.
- Identifier forms: ` + "`FuncName`" + `, ` + "`Receiver.Method`" + `, ` + "`StructName`" + `/` + "`InterfaceName`" + `, ` + "`pkg.Name`" + `.

## Discovery from inside a session

The MCP tool descriptions are deliberately terse. For full per-tool detail (examples, ops, limitations), shell out to:

` + "```" + `
go-surgeon discovery              # grouped list
go-surgeon discovery <tool>       # tool detail
go-surgeon discovery <tool>.<op>  # op detail (e.g. patch.function)
` + "```" + `

Re-running ` + "`go-surgeon skill --out .claude/skills/go-surgeon/`" + ` regenerates this file from the live catalog.
`
