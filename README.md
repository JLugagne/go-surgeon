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

The unified `patch` tool makes scoped edits with a `target` selector: edit inside a function body, add/rename/retype a single struct field, add or remove a single interface method and regenerate the mock — all without re-emitting the whole declaration.

---

## When go-surgeon helps vs. when Edit is fine

**Use go-surgeon for:**
- Exploring unfamiliar Go code (`symbol body=true context=file` gives you a function body + full file outline in one call)
- Structural edits: adding/renaming struct fields, interface methods, managing imports
- Batch edits across many functions or files in one atomic operation
- Multi-step sessions where AST validation prevents compounding errors
- Any edit where import management matters

**Edit is fine or better when:**
- Single-line tweak in a file you already have open and know well
- Files outside Go: `.yaml`, `.md`, `.sh`, `Dockerfile`, etc.
- One-off prototyping where you don't need AST guarantees
- 3-line changes where the 200–500 ms MCP overhead isn't amortized

> Each go-surgeon MCP call adds ~200–500 ms overhead vs a direct Edit. The break-even is roughly 5+ structural edits, or any task requiring AST-level guarantees (import management, type-aware renames, struct/interface modifications). For a single 3-line tweak in a file you already know, Edit is faster.

---

## Install

**Linux / macOS**

```bash
curl -fsSL https://raw.githubusercontent.com/JLugagne/go-surgeon/main/install.sh | sh
```

Installs the latest release binary to `~/.local/bin` (no root required). Override with `INSTALL_DIR`:

```bash
INSTALL_DIR=/usr/local/bin curl -fsSL https://raw.githubusercontent.com/JLugagne/go-surgeon/main/install.sh | sh
```

**Homebrew / Scoop / Windows** — coming soon.

---

## Quick Start

```bash
# Verify installation
go-surgeon --version

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

Tools over stdio, grouped by purpose:

| Tools | Purpose |
|---|---|
| `overview`, `symbol` | Explore packages and look up symbols — **replaces Read / Grep / Glob** |
| `find_definition`, `find_references`, `rename_symbol` | Type-aware cross-package symbol lookup and rename — powered by `go/packages` |
| `create`, `update`, `delete` | Add, replace, or remove a file, function, or struct by AST identifier — **replaces Edit / Write** |
| `patch` | Unified surgical editor — one tool, five targets (`function`, `struct`, `interface`, `file`, `decl`). Scoped in-place edits without re-emitting whole declarations |
| `insert_call` | Insert a single statement into a function body (`before-return`, `end-of-body`, or `after:<marker>`); auto-lifts out of nested scopes |
| `add_interface`, `update_interface`, `delete_interface` | Manage interfaces with auto-generated (and auto-deleted) mocks |
| `implement`, `mock`, `extract_interface` | Generate stubs, standalone mocks, and extract interfaces from structs |
| `test`, `tag` | Generate test skeletons and struct field tags |
| `build_check`, `test_run` | Compile-verify and run tests in-loop; `affected_by=<file>` narrows to the file's reverse-dep closure |
| `execute_plan` | Run up to 15 edits atomically from a YAML plan — supports every action type including every `patch` target |
| `batch_query` | Run up to 10 read-only queries (`symbol` / `overview` / `find_definition` / `find_references`) in one round-trip |
| `describe_tool` | Queryable catalog of every tool — no args for the grouped list, `name=X` for detail |

Every write tool supports `preview=true` to return a unified diff without writing. Errors carry a structured `{code, message}` in StructuredContent so agents can retry on `CONFLICT`, `NOT_FOUND`, `PATCH_FAILED`, etc. without string-matching.

See [`USAGE.md`](USAGE.md) for the full parameter reference.

---

## Highlighted features

### `symbol body=true context=file` — explore a 1000-line file in 4 calls

`symbol` with `body=true` and `context="file"` returns the full body of the target function **and** an outline of every sibling declaration in the same file — in one call.

```
symbol(query="BookHandler.Create", body=true, context="file")
```

This replaces what used to be: read the file, grep for the function, read again with offset, grep for related symbols. Measured on a 1000-line file: **4 calls instead of 15**.

Use this as your first move when entering any unfamiliar file — you get the implementation you care about plus a map of everything around it.

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

### `patch` — one tool, five targets, surgical edits

Classic AST edit tools make you resend the whole declaration to change one line. The unified `patch` tool applies scoped, text-match-or-line-targeted edits to a single function body, struct, interface, whole file, or const/var — all atomic, all `goimports`-aware, all optionally previewable as a diff.

**Target a function body:**

```
patch(
  target="function",
  file="internal/catalog/app/commands/book_handler.go",
  identifier="BookHandler.Create",
  patches=[
    {op: "replace", match: "return err", replace: "return fmt.Errorf(\"create book: %w\", err)"},
  ],
)
```

Line targeting (`at_line`, `from_line`/`to_line`) is preferred when you have line numbers from a build error or stack trace — faster and unambiguous. Text matching (`match`, `match_regex`) is the fallback.

**Target a struct's field list:**

```
patch(
  target="struct",
  file="internal/catalog/domain/user.go",
  identifier="User",
  patches=[
    {op: "add_field", name: "Email", type: "string", tag: "json:\"email\""},
    {op: "rename_field", from: "Name", to: "DisplayName"},
    {op: "remove_field", name: "LegacyID"},
  ],
)
```

**Target an interface's method list (with automatic mock regeneration):**

```
patch(
  target="interface",
  file="internal/catalog/domain/repositories/book/book.go",
  identifier="BookRepository",
  mock_file="internal/catalog/domain/repositories/book/booktest/mock.go",
  mock_name="MockBookRepository",
  patches=[
    {op: "add_method", signature: "Archive(ctx context.Context, id BookID) error"},
  ],
)
```

Other targets: `file` for cross-function batch substitutions, `decl` for const/var values. See [`USAGE.md`](USAGE.md) for the full operation catalog per target.

### `rename_symbol` — type-aware rename across the module

Renaming a symbol with `sed` or generic Edit is how you rename the wrong thing: same-named identifiers in other packages, shadowing variables, method receivers that happen to share the name. `rename_symbol` resolves the target via `go/packages` and rewrites only the identifiers that bind to the same `types.Object`.

```
rename_symbol(name="BookRepo", new_name="BookRepository")
rename_symbol(name="Handle", new_name="Serve", receiver="BookHandler")
rename_symbol(name="Config", new_name="Settings", preview=true)
```

Refuses export-status flips and in-scope name collisions. `find_references` (same resolver) lets you preview impact without touching files; set `include_definition=true` to see the declaration alongside the uses.

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

# Find every reference to a symbol, type-aware
go-surgeon find-references BookRepository --include-definition

# Rename a symbol and every reference across the module
go-surgeon rename-symbol BookRepo BookRepository --preview

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

> ⚠️ **Edits modify your source code directly.** Use `--dry-run` (CLI) or `preview=true` (MCP `patch` and other write tools) to see the unified diff before applying.

- **`--dry-run` / `--diff`** (CLI) prints the unified diff for every change without writing to disk
- **`preview=true`** (MCP `patch` and other write tools) returns the diff without writing
- **Atomic operations** — each edit either fully succeeds or returns a structured error; `patch` aborts the whole batch on any single failure
- **Brace and body guards** — `patch` on `function` rejects edits with unbalanced braces or that would erase the entire function body, with hints pointing at the correct syntax
- **No silent fallbacks** — failed lookups produce explicit errors with hints (`Hint: use 'go-surgeon symbol X' to locate it`)
- **Mocks stay in sync** — `delete_interface` with `delete_mock=true` also removes the mock struct, its methods, and the compile-time assertion. Without it, the broken assertion forces explicit cleanup by design

**Performance note:** each MCP call adds ~200–500 ms overhead compared to a direct file edit. This cost is amortized when you're doing structural work (imports, renames, multi-step edits). For a single short tweak in a file you already know, a direct Edit is faster. See [When go-surgeon helps vs. when Edit is fine](#when-go-surgeon-helps-vs-when-edit-is-fine).

---

## Works well with scaffor

Same philosophy, different scope:

- **[scaffor](https://github.com/JLugagne/scaffor)** — deterministic scaffolding. Generate the file structure of a new feature.
- **go-surgeon** — deterministic editing. Modify the code that already exists.

Use scaffor to bootstrap, go-surgeon to evolve. Both ship as MCP servers.

---

## Installation

```bash
# curl installer (Linux / macOS) — recommended
curl -fsSL https://raw.githubusercontent.com/JLugagne/go-surgeon/main/install.sh | sh

# Build from source
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
