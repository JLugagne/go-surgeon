<div align="center">

# go-surgeon

### **Deterministic Go code editing for LLM agents. No text patching. No broken builds.**

Your agent shouldn't edit Go with Edit, Read, Grep, or Bash.

Go isn't just text, it's a tree of declarations. go-surgeon gives your agent a real AST-based toolkit — precise symbol lookup, structural edits, automatic `goimports` — exposed as an MCP server it uses instead of generic file tools.

**One tool call per edit. Valid Go every time.**

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)](https://golang.org)
[![MCP](https://img.shields.io/badge/MCP-ready-8A2BE2)](https://modelcontextprotocol.io)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

[**Quick Start**](#quick-start) • [**Why**](#why-go-surgeon) • [**MCP Tools**](#-mcp-tools) • [**CLI**](#cli-usage) • [**Workflow**](#typical-agent-workflow)

</div>

---

## The problem nobody admits

Ask your agent to update a single function in a 200-line Go file. Watch what happens:

1. It `Read`s the file, finds the function, plans the edit
2. It calls `Edit` with a string replacement — misses a trailing tab
3. The patch fails. It re-reads the file. Tries again with the whole function body
4. This time it forgets the `context.Context` import
5. `go build` fails. It edits the import block — badly. Curly brace drift.
6. Three turns later you have a working file and no idea what changed

Every Go dev using an LLM agent has lived this. The problem isn't the model's reasoning — **text-level patching is fundamentally wrong for a structured language**. Indentation, imports, braces: these aren't content, they're grammar. And grammar breaks loudly.

## The fix

go-surgeon exposes Go-specific, AST-aware tools — via MCP or CLI — that the agent uses instead of Edit/Read/Grep on `.go` files:

```
update(object="func", file="internal/catalog/domain/book.go", identifier="NewBook", content="""
func NewBook(title, author string) (*Book, error) {
    return &Book{Title: title, Author: author}, nil
}
""")
```

- Located by **AST identifier** (`NewBook`, `Book.Validate`, `BookRepository`) — not line numbers, not regex
- Replaced by **byte range** — comments, godoc, and surrounding code stay intact
- `goimports` runs **automatically** — no import drift, no indentation errors

One tool call. Atomic success or clear error. The file stays valid Go.

---

## Why go-surgeon

### 1. It replaces generic file tools for Go — everywhere

The MCP server ships with instructions telling the agent: **for any `.go` file, use these tools instead of Edit/Write/Read/Grep/Glob/Bash**. No more `sed` on Go source. No more `grep -r` that misses method receivers. No more `Edit` that forgets imports.

### 2. Edits are atomic

Every tool is a structured operation: `create`, `update`, `delete`, `insert_call`. Either it succeeds or you get a clear error like `ERROR (update func): node 'Book.Validate' not found in ...`. No silent half-edits. No "it kind of worked".

### 3. Your agent never manages imports or formatting

Content is raw Go source — no package declaration, no imports. `goimports` runs on every mutation. An entire category of agent mistakes, permanently eliminated.

### 4. Interfaces and mocks stay in sync

`add_interface` / `update_interface` regenerate a function-field mock atomically. The compile-time assertion (`var _ Repo = (*MockRepo)(nil)`) blocks drift. `extract_interface` pulls an interface out of an existing struct in one command — with optional mock generation.

### 5. Explore third-party code without `find` in `$GOMODCACHE`

`graph` and `symbol` take a `module` parameter: point them at `github.com/spf13/cobra`, they'll return its package tree or the body of any symbol — resolved via `go/packages`. Your agent reads dependency source the same way it reads yours.

### 6. Batch 15 edits in one atomic plan

`execute_plan` takes a YAML of up to 15 actions and runs them as one operation. Ideal for multi-step refactors: add a struct, update three methods, create a mock, insert a call — one tool invocation, one success/failure boundary.

---

## Quick Start

```bash
# Build
go build -o go-surgeon ./cmd/go-surgeon

# Run as MCP server (stdio)
go-surgeon mcp

# Or use the CLI directly
go-surgeon graph
go-surgeon symbol BookHandler.Handle --body
```

### Configure for Claude Code / Cursor

```json
{
  "mcpServers": {
    "go-surgeon": {
      "command": "go-surgeon",
      "args": ["mcp"]
    }
  }
}
```

Once connected, the server advertises its own instructions telling the client to use go-surgeon tools for every operation on `.go` files — no prompt engineering required on your side.

---

## 🔌 MCP Tools

### Exploration — replaces `Read` / `Grep` / `Glob` / `find`

| Tool | Purpose |
|---|---|
| `graph` | Walk packages. Options: `symbols`, `summary`, `deps`, `focus`, `recursive`, `depth`, `exclude`, `token_budget`, `module` |
| `symbol` | AST lookup by `Name`, `Receiver.Method`, or `pkg.Name`. `body=true` returns the full source with line numbers |

Both accept `module="github.com/org/repo"` to explore a dependency's source instead of the current project.

### Editing — replaces `Edit` / `Write`

| Tool | Purpose |
|---|---|
| `create` | `object: file \| func \| struct` — add new code |
| `update` | `object: file \| func \| struct` — replace by AST identifier, with optional `doc` / `strip_doc` |
| `delete` | `object: func \| struct` — remove a declaration (struct also removes its methods) |
| `insert_call` | Insert a single statement into a function body at `before-return`, `end-of-body`, or `after:<marker>` — idempotent |
| `execute_plan` | Run up to 15 actions atomically from a YAML plan |

### Interfaces & mocks

| Tool | Purpose |
|---|---|
| `add_interface` | Create an interface and auto-generate a function-field mock with compile-time assertion |
| `update_interface` | Update an interface and regenerate its mock in lockstep |
| `delete_interface` | Remove an interface (mock is not auto-deleted — by design, so build fails until you clean up) |
| `implement` | Generate missing method stubs on a struct for any interface (stdlib, third-party, project-local) |
| `mock` | Standalone mock for an interface you don't own |
| `extract_interface` | Extract an interface from an existing struct's exported methods, with optional mock |

### Code generation

| Tool | Purpose |
|---|---|
| `test` | Generate a table-driven test skeleton for a function or method |
| `tag` | Add/update struct field tags — `auto=json`, `auto=bson`, or `field`+`set` for a single field |

---

## Highlighted features

### `execute_plan` — atomic multi-step refactors

Refactoring a feature often means changing a struct, updating three methods, regenerating a mock, and wiring a new call. Doing this as 8 separate `Edit` operations is where agents drift the most.

```yaml
actions:
  - action: update_struct
    file: internal/catalog/domain/book.go
    identifier: Book
    content: |
      type Book struct {
          ID        BookID
          Title     string
          Author    string
          Status    BookStatus
          CreatedAt time.Time
      }
  - action: update_func
    file: internal/catalog/domain/book.go
    identifier: NewBook
    content: |
      func NewBook(title, author string, status BookStatus) (*Book, error) {
          return &Book{ID: NewBookID(), Title: title, Author: author, Status: status}, nil
      }
  - action: update_interface
    file: internal/catalog/domain/repositories/book/book.go
    identifier: BookRepository
    mock_file: internal/catalog/domain/repositories/book/booktest/mock.go
    mock_name: MockBookRepository
    content: |
      type BookRepository interface {
          Create(ctx context.Context, book domain.Book) error
          UpdateStatus(ctx context.Context, id BookID, status BookStatus) error
      }
  - action: insert_call
    file: internal/catalog/app/init.go
    identifier: NewApp
    content: handlers.RegisterBookStatusHandler(mux, repo)
    position: before-return
```

One tool call. One success or one rollback. No drift between steps.

### `module` — read third-party code the right way

Instead of your agent shelling into `$GOMODCACHE` with `find` and `cat`:

```
graph(module="github.com/spf13/cobra", symbols=true)
symbol(query="Command.Execute", body=true, module="github.com/spf13/cobra")
```

Resolved via `go/packages`. Same output format as your own project. Works for stdlib, third-party, and project-local alike.

---

## CLI usage

Everything the MCP server exposes is also available from the CLI — useful for scripting, CI, and quick exploration:

```bash
# Orient yourself
go-surgeon graph
go-surgeon graph --symbols --dir internal/catalog/domain

# Read a symbol
go-surgeon symbol BookHandler.Handle --body

# Edit a function (stdin = raw Go, no package/imports)
cat <<'EOF' | go-surgeon update-func --file internal/catalog/domain/book.go --id NewBook
func NewBook(title, author string) (*Book, error) {
    return &Book{Title: title, Author: author}, nil
}
EOF

# Generate stubs for an interface
go-surgeon implement io.ReadCloser --receiver "*MyReader" --file internal/pkg/reader.go
```

See [`USAGE.md`](USAGE.md) for the full CLI reference.

---

## Typical agent workflow

From the agent's perspective, a feature implementation now looks like:

```
# 1. Orient without reading files
graph(focus="internal/catalog/outbound")

# 2. Find a pattern to follow
symbol(query="PgBookRepo.Create", body=true)

# 3. Read what's about to change
symbol(query="BookHandler.Handle", body=true)

# 4. Make the changes atomically
execute_plan(plan="""
  actions:
    - action: add_struct
      ...
    - action: add_interface
      ...
    - action: update_func
      ...
""")

# 5. Generate a test skeleton
test(file="internal/catalog/domain/book.go", identifier="NewBook")
```

At no point does the agent count tabs, manage imports, reason about line numbers, or `grep` through files.

---

## Installation

```bash
# From source
git clone https://github.com/JLugagne/go-surgeon.git
cd go-surgeon && go build -o go-surgeon ./cmd/go-surgeon

# Shell completion
go-surgeon completion bash > /etc/bash_completion.d/go-surgeon
go-surgeon completion zsh  > "${fpath[1]}/_go-surgeon"
```

---

## Works well with scaffor

Same philosophy, different scope:

- **[scaffor](https://github.com/JLugagne/scaffor)** — deterministic scaffolding. Generate the file structure of a new feature.
- **go-surgeon** — deterministic editing. Modify the code that already exists.

Use scaffor to bootstrap, go-surgeon to evolve. Both ship as MCP servers.

---

## Going further

- [`USAGE.md`](USAGE.md) — full CLI reference
- [`examples/`](examples) — real editing sessions

---

<div align="center">

[MIT License](LICENSE) · Feedback and contributions welcome

</div>
