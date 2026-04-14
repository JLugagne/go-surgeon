<div align="center">

# go-surgeon

### **Deterministic Go code editing for LLM agents. No text patching. No broken builds.**

Your agent shouldn't edit Go with Edit, Read, Grep, or Bash.

Go isn't just text, it's a tree of declarations. go-surgeon gives your agent a real AST-based toolkit — precise symbol lookup, structural edits, automatic `goimports` — exposed as an MCP server it uses instead of generic file tools.

**One tool call per edit. Valid Go every time.**

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)](https://golang.org)
[![MCP](https://img.shields.io/badge/MCP-ready-8A2BE2)](https://modelcontextprotocol.io)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

[**Quick Start**](#quick-start) • [**Why**](#why-go-surgeon) • [**MCP**](#-mcp-server) • [**Highlights**](#highlighted-features) • [**Safety**](#-safety)

</div>

---

## The problem nobody admits

Ask your agent to update a single function in a 200-line Go file. Watch what happens:

1. It `Read`s the file, finds the function, plans the edit
2. It calls `Edit` with a string replacement — misses a trailing tab
3. The patch fails. It re-reads the file. Tries again with the whole function body
4. This time it forgets the `context.Context` import
5. `go build` fails. It edits the import block — badly. Curly brace drift.
6. Three turns later you have a working file and no idea what changed.

Every Go dev using an LLM agent has lived this. The problem isn't the model's reasoning — **text-level patching is fundamentally wrong for a structured language**. Indentation, imports, braces: these aren't content, they're grammar. And grammar breaks loudly.

## The fix

```
update(object="func", file="internal/catalog/domain/book.go", identifier="NewBook", content="""
func NewBook(title, author string) (*Book, error) {
    return &Book{Title: title, Author: author}, nil
}
""")
```

```
✅ SUCCESS (update func): Updated NewBook in internal/catalog/domain/book.go
```

Located by **AST identifier**. Replaced by **structural edit**. Imports handled by **`goimports`** automatically.
The agent stops counting tabs and starts shipping logic.

---

## Why go-surgeon

### 1. It replaces generic file tools for Go — everywhere

The MCP server ships with instructions telling the agent: **for any `.go` file, use these tools instead of Edit / Write / Read / Grep / Glob / Bash**. No more `sed` on Go source. No more `grep -r` that misses method receivers. No more `Edit` that forgets imports.

### 2. Edits are atomic, not conversational

Every tool is a structured operation. Either it succeeds or you get a clear error like `ERROR (update func): node 'Book.Validate' not found in ...`. No silent half-edits. No "it kind of worked".

### 3. Your agent never manages imports or formatting

Content is raw Go source — no package declaration, no imports, no indentation. `goimports` runs on every mutation. An entire category of agent mistakes, permanently eliminated.

### 4. Interfaces and mocks stay in sync

`add_interface` and `update_interface` regenerate a function-field mock atomically. The compile-time assertion (`var _ Repo = (*MockRepo)(nil)`) blocks drift. `extract_interface` pulls an interface out of an existing struct in one command.

### 5. Edits can be as granular as a single field or line

`patch_function` makes text-match edits inside a function body. `patch_struct` adds, renames, or retypes a single field. `patch_interface` adds or removes a single method and regenerates the mock. No more re-emitting a whole declaration to change one thing.

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

Configure your MCP client (example for Claude Code / Cursor):

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

The server auto-advertises instructions telling the agent to use go-surgeon tools for every operation on `.go` files — no prompt engineering required on your side.

---

## 🔌 MCP Server

```bash
go-surgeon mcp
```

18 tools over stdio, grouped by purpose:

| Tools | Purpose |
|---|---|
| `graph`, `symbol` | Explore packages and look up symbols — **replaces Read / Grep / Glob** |
| `create`, `update`, `delete` | Add, replace, or remove a file, function, or struct by AST identifier — **replaces Edit / Write** |
| `patch_function`, `patch_struct`, `patch_interface` | Surgical in-place edits: one line inside a body, one struct field, one interface method — **avoids re-emitting whole declarations** |
| `insert_call` | Insert a single statement into a function body (`before-return`, `end-of-body`, or `after:<marker>`) |
| `add_interface`, `update_interface`, `delete_interface` | Manage interfaces with auto-generated (and auto-deleted) mocks |
| `implement`, `mock`, `extract_interface` | Generate stubs, standalone mocks, and extract interfaces from structs |
| `test`, `tag` | Generate test skeletons and struct field tags |
| `execute_plan` | Run up to 15 edits atomically from a YAML plan |

All `patch_*` tools support `preview=true` to return a diff without writing.

See [`USAGE.md`](USAGE.md) for the full parameter reference.

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
          Status    BookStatus
          CreatedAt time.Time
      }
  - action: update_func
    file: internal/catalog/domain/book.go
    identifier: NewBook
    content: |
      func NewBook(title string, status BookStatus) (*Book, error) {
          return &Book{ID: NewBookID(), Title: title, Status: status}, nil
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

### `patch_function` / `patch_struct` / `patch_interface` — edit without re-emitting

Classic AST edit tools make you resend the whole declaration to change one line. The `patch_*` tools apply surgical, text-match edits scoped to a single function body, struct, or interface — all atomic, all `goimports`-aware, all optionally previewable as a diff.

```
patch_struct(
  file="internal/catalog/domain/user.go",
  identifier="User",
  patches=[
    {op: "add_field", name: "Email", type: "string", tag: "json:\"email\""},
    {op: "rename_field", from: "Name", to: "DisplayName"},
    {op: "remove_field", name: "LegacyID"},
  ],
)
```

```
patch_interface(
  file="internal/catalog/domain/repositories/book/book.go",
  identifier="BookRepository",
  mock_file="internal/catalog/domain/repositories/book/booktest/mock.go",
  mock_name="MockBookRepository",
  patches=[
    {op: "add_method", signature: "Archive(ctx context.Context, id BookID) error"},
  ],
)
```

`patch_function` does the same for function bodies, with `replace`, `insert_before`, `insert_after`, `delete`, and `wrap` operations — including RE2 regex matching when literal text isn't enough. The agent sends 3 lines of change, not 50 lines of function body.

### `module` — read third-party code the right way

Instead of your agent shelling into `$GOMODCACHE` with `find` and `cat`:

```
graph(module="github.com/spf13/cobra", symbols=true)
symbol(query="Command.Execute", body=true, module="github.com/spf13/cobra")
```

Resolved via `go/packages`. Same output format as your own project. Works for stdlib, third-party, and project-local interfaces alike.

---

## CLI

Everything the MCP server exposes is also available from the CLI — useful for scripting, CI, and quick exploration:

```bash
# Orient yourself
go-surgeon graph --symbols --dir internal/catalog/domain

# Read a symbol
go-surgeon symbol BookHandler.Handle --body

# Edit a function (stdin = raw Go, no package/imports)
cat <<'EOF' | go-surgeon update-func --file internal/catalog/domain/book.go --id NewBook
func NewBook(title, author string) (*Book, error) {
    return &Book{Title: title, Author: author}, nil
}
EOF

# Generate stubs for an interface you don't own
go-surgeon implement io.ReadCloser --receiver "*MyReader" --file internal/pkg/reader.go
```

Pass `--dry-run` on any command to preview changes as a unified diff without writing to disk.

See [`USAGE.md`](USAGE.md) for the full CLI reference.

---

## 🔒 Safety

> ⚠️ **Edits modify your source code directly.** Use `--dry-run` (CLI) or `preview=true` (MCP `patch_*` tools) to see the unified diff before applying.

- **`--dry-run` / `--diff`** (CLI) prints the unified diff for every change without writing to disk
- **`preview=true`** (MCP `patch_function` / `patch_struct` / `patch_interface`) returns the diff without writing
- **Atomic operations** — each edit either fully succeeds or returns a structured error; `patch_*` tools abort the whole batch on any single failure
- **No silent fallbacks** — failed lookups produce explicit errors with hints (`Hint: use 'go-surgeon symbol X' to locate it`)
- **Mocks stay in sync** — `delete_interface` with `delete_mock=true` also removes the mock struct, its methods, and the compile-time assertion. Without it, the broken assertion forces explicit cleanup by design

---

## Works well with scaffor

Same philosophy, different scope:

- **[scaffor](https://github.com/JLugagne/scaffor)** — deterministic scaffolding. Generate the file structure of a new feature.
- **go-surgeon** — deterministic editing. Modify the code that already exists.

Use scaffor to bootstrap, go-surgeon to evolve. Both ship as MCP servers.

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

## Going further

- [`USAGE.md`](USAGE.md) — full command reference for MCP and CLI
- [`examples/`](examples) — real editing sessions

---

<div align="center">

[MIT License](LICENSE) · Feedback and contributions welcome

</div>
