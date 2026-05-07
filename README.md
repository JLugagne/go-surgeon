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

`interface action=add` and `interface action=update` regenerate a function-field mock atomically. The compile-time assertion (`var _ Repo = (*MockRepo)(nil)`) blocks drift. `derive kind=interface_from_type` pulls an interface out of an existing struct in one command.

### 5. Edits can be as granular as a single field or line

The unified `patch` tool makes scoped edits with a `target` selector: edit inside a function body, add/rename/retype a single struct field, add or remove a single interface method and regenerate the mock — all without re-emitting the whole declaration.

### 6. Type-aware references and renames across the module

`find_definition`, `find_references`, and `rename_symbol` resolve the target via `go/packages` so they only touch identifiers that bind to the same `types.Object` — not other symbols that happen to share a name. They also accept `module=` to resolve into a dependency.

---

## When go-surgeon helps vs. when Edit is fine

**Use go-surgeon for:**
- Exploring unfamiliar Go code (`symbol body=true context=file` gives you a function body + full file outline in one call)
- Resolving a `file:line` build/stack diagnostic to a declaration (`symbol file=... at_line=...`)
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

**Self-update** — once installed, keep the binary current with:

```bash
go-surgeon upgrade
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
| `overview`, `symbol` | Explore packages and look up symbols — **replaces Read / Grep / Glob**. `symbol` also resolves a `file:at_line` diagnostic directly to its declaration. |
| `find_definition`, `find_references`, `rename_symbol` | Type-aware cross-package symbol lookup and rename — powered by `go/packages`. All three accept `module=` to resolve into a dependency. |
| `create`, `update`, `delete` | Add, replace, or remove a file, function, or struct by AST identifier — **replaces Edit / Write**. `object="auto"` infers from the content; `delete object="file"` removes the file from disk. |
| `patch` | Unified surgical editor — one tool, five targets (`function`, `struct`, `interface`, `file`, `decl`). Scoped in-place edits without re-emitting whole declarations. Function ops include `replace`, `insert_before`/`insert_after`, `delete`, `wrap`, and `set_signature` (rewrite params/returns without touching the body). |
| `patch` with `items: [{...}]` | Apply many `patch` operations to many targets in a single atomic call — useful when one refactor touches dozens of functions or structs. |
| `insert_call` | Insert a single statement into a function body (`before-return`, `end-of-body`, or `after:<marker>`); auto-lifts out of nested scopes |
| `interface` (`action=add\|update\|delete`) | Manage interfaces with auto-generated (and auto-deleted) mocks |
| `derive` (`kind=impl_from_interface\|mock_from_interface\|interface_from_type`) | Generate stubs, standalone mocks, and extract interfaces from structs |
| `test`, `tag` | Generate test skeletons and struct field tags |
| `build_check`, `test_run` | Compile-verify and run tests in-loop. Both accept `affected_by=<file>` to narrow to the file's reverse-dep closure; `test_run` also accepts `symbols=["pkg.MyFunc"]` to auto-resolve owning packages and build a `-run` filter, plus `verbosity=summary` for compact output on large suites. |
| `execute_plan` | Run up to 15 edits atomically from a YAML/JSON plan — supports every action type including every `patch` target |
| `batch_query` | Run up to 10 read-only queries (`symbol` / `overview` / `find_definition` / `find_references`) in one round-trip |
| `describe_tool` | Queryable catalog of every tool — no args for the grouped list, `name=X` for detail |

Every write tool supports `preview=true` to return a unified diff without writing. Errors carry a structured `{code, message}` in StructuredContent so agents can retry on `CONFLICT`, `NOT_FOUND`, `PATCH_FAILED`, `PATCH_REPLACE_NOT_APPLIED`, `PATCH_DROPPED_CONTENT`, `PATCH_PRODUCES_INVALID_GO`, etc. without string-matching.

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

### `symbol file=… at_line=…` — resolve a build error to a declaration

When `build_check` or a stack trace gives you `internal/foo/bar.go:142`, you don't need to look up the symbol name. Pass the line and `symbol` returns the outermost named declaration that spans it:

```
symbol(file="internal/foo/bar.go", at_line=142, body=true)
```

Mutually exclusive with `query`/`pattern`. Saves the "grep for the function around this line" step entirely.

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

One tool call. One success or one rollback. No drift between steps. Every individual `patch_*` action type is also accepted, so atomic multi-step plans can mix in-place patches with whole-declaration replacements.

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

Line targeting (`at_line`, `from_line`/`to_line`) is preferred when you have line numbers from a build error or stack trace — faster and unambiguous. Text matching (`match`, `match_regex`) is the fallback. Use `occurrence=-1` to apply a patch to every match instead of just the first.

**Rewrite a function's signature without touching its body:**

```
patch(
  target="function",
  file="internal/catalog/app/commands/book_handler.go",
  identifier="BookHandler.Create",
  patches=[
    {op: "set_signature",
     params: ["ctx context.Context", "input CreateBookInput"],
     returns: "(*Book, error)"},
  ],
)
```

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

### `patch items: [{...}]` — fan one refactor across many declarations atomically

When the same shape of change has to land on dozens of structs or functions (e.g. add a `CreatedAt` field everywhere, or wrap every `return err` in a domain package), `patch target=struct` and `patch target=function` accept an `items: [{file, identifier, patches}, ...]` list and apply all of them in one transaction. If any one item fails, nothing is written.

### `rename_symbol` — type-aware rename across the module

Renaming a symbol with `sed` or generic Edit is how you rename the wrong thing: same-named identifiers in other packages, shadowing variables, method receivers that happen to share the name. `rename_symbol` resolves the target via `go/packages` and rewrites only the identifiers that bind to the same `types.Object`.

```
rename_symbol(name="BookRepo", new_name="BookRepository")
rename_symbol(name="Handle", new_name="Serve", receiver="BookHandler")
rename_symbol(name="Config", new_name="Settings", preview=true)
```

Refuses export-status flips and in-scope name collisions. `find_references` (same resolver) lets you preview impact without touching files; set `include_definition=true` to see the declaration alongside the uses.

### `build_check` / `test_run` — verify in-loop, scoped to what changed

After an edit, run the compiler or the tests directly without leaving the agent loop:

```
build_check(affected_by="internal/catalog/domain/book.go", tests=true)
test_run(symbols=["catalog.NewBook", "catalog.Book.Validate"], verbosity="summary")
```

`affected_by=<file>` narrows the build/test to the file's owning package plus its in-module reverse-dep closure — orders of magnitude faster than `./...` on a monorepo. `test_run` additionally accepts `symbols=["pkg.Func", ...]`: it resolves the owning packages and synthesizes a `-run ^(TestFunc|...)$` filter for you. `verbosity="summary"` keeps the structured payload around 1KB even on huge suites; `auto` (default) flips to summary past 50 tests.

### `module=` — read third-party code the right way

Instead of your agent shelling into `$GOMODCACHE` with `find` and `cat`:

```
overview(module="github.com/spf13/cobra", symbols=true)
symbol(query="Command.Execute", body=true, module="github.com/spf13/cobra")
find_references(name="Execute", receiver="Command", module="github.com/spf13/cobra")
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

# Self-update to the latest release
go-surgeon upgrade
```

Pass `--dry-run` on any command to preview changes as a unified diff without writing to disk.

> The granular `patch` family (in-place edits to function/struct/interface/file/decl) is **MCP-only** for now. The CLI exposes the whole-declaration `update-func` / `update-struct` / `update-interface` commands instead.

See [`USAGE.md`](USAGE.md) for the full CLI reference.

---

## 🔒 Safety

> ⚠️ **Edits modify your source code directly.** Use `--dry-run` (CLI) or `preview=true` (MCP `patch` and other write tools) to see the unified diff before applying.

- **`--dry-run` / `--diff`** (CLI) prints the unified diff for every change without writing to disk
- **`preview=true`** (MCP `patch` and other write tools) returns the diff without writing
- **Atomic operations** — each edit either fully succeeds or returns a structured error; `patch` aborts the whole batch on any single failure
- **Brace and body guards** — `patch` on `function` rejects edits with unbalanced braces or that would erase the entire function body, with hints pointing at the correct syntax
- **Post-splice validation** — `patch` on `function`/`file` re-parses the result and refuses replacements whose substring went missing or that silently dropped declarations (`PATCH_REPLACE_NOT_APPLIED`, `PATCH_DROPPED_CONTENT`); the file is left untouched
- **No silent fallbacks** — failed lookups produce explicit errors with hints (`Hint: use 'go-surgeon symbol X' to locate it`)
- **Mocks stay in sync** — `interface action=delete` with `delete_mock=true` also removes the mock struct, its methods, and the compile-time assertion. Without it, the broken assertion forces explicit cleanup by design

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

# Self-update an existing install
go-surgeon upgrade

# Shell completion
go-surgeon completion bash > /etc/bash_completion.d/go-surgeon
go-surgeon completion zsh  > "${fpath[1]}/_go-surgeon"
```

---

## Going further

- [`USAGE.md`](USAGE.md) — full command reference for MCP and CLI
- [`AI_INSTRUCTIONS.md`](AI_INSTRUCTIONS.md) — drop-in instructions for Cursor / Claude / Copilot system prompts

---

<div align="center">

[MIT License](LICENSE) · Feedback and contributions welcome

</div>
