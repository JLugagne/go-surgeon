# go-surgeon Usage Guide

Complete reference for every command and flag exposed by go-surgeon.

go-surgeon has two surfaces:

- **MCP server** — the primary way for AI agents (Claude Code, Cursor, etc.) to use go-surgeon. Exposes a compact tool API designed for LLMs.
- **CLI** — one subcommand per action, designed for scripting, CI, and direct human use.

Both surfaces are built on the same domain engine, so behavior is identical.

---

## Table of contents

- [Getting started](#getting-started)
- [MCP server](#mcp-server)
  - [Connecting from Claude Code / Cursor](#connecting-from-claude-code--cursor)
  - [MCP tools reference](#mcp-tools-reference)
  - [Patch tools (MCP only)](#patch-tools-mcp-only)
- [CLI reference](#cli-reference)
  - [Global flags](#global-flags)
  - [Exploration: `graph`, `symbol`](#exploration-graph-symbol)
  - [Type-aware refs + rename: `find-definition`, `find-references`, `rename-symbol`](#find-definition-find-references-rename-symbol)
  - [Editing: `create-file`, `add-func`, `update-func`, …](#editing-create-file-add-func-update-func-)
  - [Targeted inserts: `insert-call`](#targeted-inserts-insert-call)
  - [Interfaces and mocks: `add-interface`, `implement`, `mock`, `extract-interface`](#interfaces-and-mocks-add-interface-implement-mock-extract-interface)
  - [Code generation: `test`, `tag`](#code-generation-test-tag)
  - [Batch plans: `execute`](#batch-plans-execute)
- [Core rules](#core-rules)

---

## Getting started

```bash
# Build from source
go build -o go-surgeon ./cmd/go-surgeon

# Verify
go-surgeon --help

# Run as MCP server (stdio)
go-surgeon mcp
```

Shell completion:

```bash
go-surgeon completion bash > /etc/bash_completion.d/go-surgeon
go-surgeon completion zsh  > "${fpath[1]}/_go-surgeon"
```

---

## MCP server

```bash
go-surgeon mcp
```

Starts an [MCP](https://modelcontextprotocol.io) server over stdio. The server advertises its own instructions telling the agent to use go-surgeon tools for any operation on `.go` files — no prompt engineering required on your side.

### Connecting from Claude Code / Cursor

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

### MCP tools reference

| Tool | Purpose |
|---|---|
| [`overview`](#overview-tool) | Explore package structure (works on the current project or any dependency) |
| [`symbol`](#symbol-tool) | Look up a function, method, or struct by name |
| [`find_definition`](#find_definition-find_references-rename_symbol-tools) | Type-aware: locate a symbol's declaration across packages |
| [`find_references`](#find_definition-find_references-rename_symbol-tools) | Type-aware: every reference to a symbol, deduplicated |
| [`rename_symbol`](#find_definition-find_references-rename_symbol-tools) | Type-aware rename across the module |
| [`create`](#create-update-delete-tools) | Add a file, function, or struct |
| [`update`](#create-update-delete-tools) | Replace a file, function, or struct by AST identifier |
| [`delete`](#create-update-delete-tools) | Remove a function or struct |
| [`patch_function`](#patch_function-tool) | Text-match edits inside a single function body |
| [`patch_struct`](#patch_struct-tool) | Granular field edits on a struct |
| [`patch_interface`](#patch_interface-tool) | Granular method edits on an interface, with mock regeneration |
| [`patch_file`](#patch_file-tool) | Whole-file text substitution with AST safety |
| [`patch_decl`](#patch_decl-tool) | Edit the value of a top-level const or var |
| [`insert_call`](#insert_call-tool) | Insert a single statement; auto-lifts out of nested scopes |
| [`add_interface`](#interface-tools) | Add an interface and auto-generate a mock |
| [`update_interface`](#interface-tools) | Update an interface and regenerate its mock |
| [`delete_interface`](#interface-tools) | Delete an interface, optionally its mock |
| [`implement`](#implement-tool) | Generate stubs for an interface you don't own |
| [`mock`](#mock-tool) | Generate a standalone mock for any interface |
| [`extract_interface`](#extract_interface-tool) | Extract an interface from an existing struct |
| [`test`](#test-tool) | Generate a table-driven test skeleton |
| [`tag`](#tag-tool) | Add or update struct field tags |
| [`build_check`](#build_check-tool) | `go build` with structured diagnostics; `affected_by=file` narrows scope |
| [`test_run`](#test_run-tool) | `go test` with compact pass/fail report; `affected_by=file` narrows scope |
| [`execute_plan`](#execute_plan-tool) | Run up to 15 edits atomically — includes every `patch_*` action |
| [`batch_query`](#batch_query-tool) | Up to 10 read-only queries in one round-trip |
| [`describe_tool`](#describe_tool-tool) | Queryable catalog of every tool |

#### `overview` tool

Explore a Go project's package structure. Exposed as `go-surgeon graph` on the CLI for historical reasons — MCP clients should use `overview`.

| Parameter | Type | Description |
|---|---|---|
| `dir` | string | Directory to walk. Default `.` |
| `symbols` | bool | Include exported symbols per file |
| `summary` | bool | Append package doc comment summary |
| `deps` | bool | Show internal package import dependencies |
| `recursive` | bool | Walk sub-packages when `symbols` is set |
| `tests` | bool | Include `_test.go` files |
| `depth` | int | Limit directory recursion depth (0 = unlimited) |
| `focus` | string | Package path for full detail; others show path only |
| `exclude` | []string | Glob patterns for directories to skip |
| `token_budget` | int | Approximate max tokens in output (0 = unlimited) |
| `module` | string | Import path of a dependency to explore (e.g. `github.com/spf13/cobra`) |

Setting `focus` automatically implies `symbols`, `summary`, and `recursive` for the focused package.

When called from a Go module root with `dir='.'` and `symbols=true`, the tool auto-flips to recursive so subpackages surface even if the root has no top-level `.go` files.

#### `symbol` tool

Look up a function, method, or struct by name.

| Parameter | Type | Description |
|---|---|---|
| `query` | string | Symbol name. Formats: `Name`, `Receiver.Method`, or `pkg.Name`. Mutually exclusive with `pattern` |
| `pattern` | string | Regex to match against declaration names; returns the list of matches |
| `body` | bool | Return the full body with line numbers. In pattern mode, bodies stream until `token_budget` is reached then degrade to signature-only |
| `context` | string | Set to `file` to additionally return an outline of every sibling declaration in the same file |
| `tests` | bool | Include `_test.go` files |
| `dir` | string | Directory to search in. Default `.` |
| `module` | string | Search in a dependency instead of the current project |
| `token_budget` | int | Max tokens for pattern+body mode (0 = unlimited) |

#### `find_definition`, `find_references`, `rename_symbol` tools

Type-aware symbol lookup and rename, powered by `go/packages`. Unlike `symbol` (which walks the AST by name), these three load the full type graph — so renames and ref lookups don't false-match identifiers that happen to share a name in other packages.

`find_definition`:

| Parameter | Type | Description |
|---|---|---|
| `name` | string | Symbol name (required) |
| `receiver` | string | Receiver type for methods (bare name, no pointer star) |
| `package` | string | Package import path or name, for disambiguation |
| `file`, `line` | string, int | Pin an exact declaration when the name is ambiguous |
| `dir` | string | Directory to load packages from. Default `.` |
| `tests` | bool | Include `_test.go` files |

`find_references` takes the same parameters plus `include_definition` (bool) — when true, the definition site is returned alongside every reference.

`rename_symbol`:

| Parameter | Type | Description |
|---|---|---|
| `name`, `new_name` | string | Current and replacement identifiers |
| `receiver`, `package`, `file`, `line` | … | Disambiguators (same as find_*) |
| `dir` | string | Directory to load packages from |
| `tests` | bool | Rewrite `_test.go` files too |
| `preview` | bool | List affected sites without writing |

Rejects renames that would flip export status (e.g. `Foo` → `foo`) or collide with an existing name in the same scope. On success, returns the list of rewritten files plus every site that changed.

#### `create`, `update`, `delete` tools

The unified editing API. Each takes an `object` parameter (`file`, `func`, `struct`, or `auto`).

**`create`** — object: `file` | `func` | `struct` | `auto`

| Parameter | Required | Description |
|---|---|---|
| `object` | yes | What to create. `auto` — infer from content: `func ...` → func, `type ... struct` → struct, else file |
| `file` | yes | Target file path |
| `content` | yes | Raw Go source. **No package declaration, no imports.** |
| `with_test` | no | Generate a test skeleton alongside (only when `object=func`) |

**`update`** — object: `file` | `func` | `struct` | `auto`

| Parameter | Required | Description |
|---|---|---|
| `object` | yes | What to update. `auto` — infer from content: `func ...` → func, `type ... struct` → struct, else file |
| `file` | yes | Target file path |
| `identifier` | yes for `func`/`struct` | `FuncName`, `Receiver.Method`, or `StructName` |
| `content` | yes | Complete new declaration |
| `doc` | no | Set or replace the doc comment (raw text, no `//` prefix) |
| `strip_doc` | no | Remove the existing doc comment |
| `with_test` | no | Generate a test skeleton alongside (only when `object=func`) |

**`delete`** — object: `func` | `struct`

| Parameter | Required | Description |
|---|---|---|
| `object` | yes | What to delete |
| `file` | yes | Target file path |
| `identifier` | yes | AST identifier |

Deleting a `struct` also removes all its methods within the same file.

#### Patch tools (MCP only)

The `patch_*` family applies **granular, atomic edits** to a single function body, struct, or interface — instead of re-emitting the whole declaration via `update`. All three share the same contract:

- Patches are applied **atomically**: if any single patch fails, nothing is written
- All standard safety applies: `goimports` runs, syntax is validated, errors are structured
- `preview: true` returns the unified diff without writing to disk

These tools are **MCP-only** for now — the CLI still uses the whole-declaration `update-*` commands.

##### `patch_function` tool

Make one or more text-match edits inside a single function body.

| Parameter | Description |
|---|---|
| `file` | Target Go file path |
| `identifier` | Function or method identifier (`FuncName` or `Receiver.Method`) |
| `patches` | Ordered list of patch operations |
| `preview` | Return the diff without writing |

**Patch ops:**

| Op | Required fields | Effect |
|---|---|---|
| `replace` | `match` or `match_regex`, `replace` | Replace matched text |
| `insert_before` | `match` or `match_regex`, `code` | Insert a line before the match |
| `insert_after` | `match` or `match_regex`, `code` | Insert a line after the match |
| `delete` | `match` or `match_regex` | Remove the matched text (or whole line) |
| `wrap` | `match` or `match_regex`, `wrap` | Replace match with `fmt.Sprintf(wrap, match)` |

- `match` is **whitespace-normalized** — indentation doesn't need to be reproduced exactly
- When a match appears multiple times, set `occurrence` (1-based) to disambiguate
- `match_regex` uses Go's RE2 engine. Patterns are capped at 1KB, must match 1..1000 times, and cannot be zero-width

**Example:**

```yaml
patches:
  - op: replace
    match: 'errors.New("invalid")'
    replace: 'fmt.Errorf("invalid input: %w", err)'
  - op: insert_before
    match: "return nil"
    code: "logger.Info(\"success\")"
```

##### `patch_struct` tool

Make granular field edits on a struct.

| Parameter | Description |
|---|---|
| `file` | Target Go file path |
| `identifier` | Struct name (e.g. `User` or `pkg.User`) |
| `patches` | Ordered list of patch operations |
| `preview` | Return the diff without writing |

**Patch ops:**

| Op | Required fields | Effect |
|---|---|---|
| `add_field` | `name`, `type` | Append a field (use `before`, `after`, or `position: first`/`last` to control placement) |
| `remove_field` | `name` | Remove a field |
| `rename_field` | `from`, `to` | Rename while preserving type, tag, and doc |
| `retype_field` | `name`, `type` | Change the type, preserving tag and doc |
| `set_tag` | `name`, `tag` | Replace the field's tag wholesale (no backticks in `tag`) |
| `set_doc` | `name`, `doc` | Set or clear the field's doc comment |

Embedded fields are addressed by their bare type name (e.g. `name: "io.Reader"`).

**Example:**

```yaml
patches:
  - op: add_field
    name: Email
    type: string
    tag: 'json:"email,omitempty"'
    after: Name
  - op: rename_field
    from: Username
    to: Handle
  - op: remove_field
    name: LegacyID
```

##### `patch_interface` tool

Make granular method edits on an interface, with automatic mock regeneration.

| Parameter | Description |
|---|---|
| `file` | Target Go file path |
| `identifier` | Interface name |
| `patches` | Ordered list of patch operations |
| `preview` | Return the diff without writing |
| `mock_file`, `mock_name` | If set, regenerate the mock when the method set changes |

**Patch ops:**

| Op | Required fields | Effect |
|---|---|---|
| `add_method` | `signature` | Add a method (e.g. `"Close() error"`) |
| `remove_method` | `name` | Remove a method |
| `rename_method` | `from`, `to` | Rename while preserving signature and doc |
| `retype_method` | `name`, `signature` | Replace the signature |
| `set_doc` | `name`, `doc` | Set or clear the method's doc comment |
| `embed` | `type` | Embed another interface (e.g. `"io.Closer"`) |
| `remove_embed` | `type` | Remove an embedded interface |

Use `before`, `after`, or `position` (add_method / embed) for placement control.

**This replaces the old "read + update_interface" workflow for adding a single method.** There is no `add_interface_method` tool — `patch_interface` with `add_method` is the equivalent.

**Example:**

```yaml
patches:
  - op: add_method
    signature: "Archive(ctx context.Context, id BookID) error"
  - op: embed
    type: io.Closer
```

##### `patch_file` tool

Whole-file text substitution with AST safety — use this when the edit spans multiple functions in the same file (e.g. renaming an internal helper that's called from many sites). The result is re-parsed and `gofmt`'d; a parse failure rejects the patch without writing.

| Parameter | Description |
|---|---|
| `file` | Target Go file path |
| `patches` | Ordered list of `{match | match_regex, replace}` entries; each sees the result of the previous |
| `preview` | Return the diff without writing |

Prefer `patch_function` when edits are scoped to one body. Prefer `rename_symbol` when the scope is a whole symbol (it's type-aware; this tool is literal text).

##### `patch_decl` tool

Edit the **value** of a top-level `const` or `var` declaration (multi-line string constants, error values, config defaults, …). Same match/replace/regex surface as `patch_function`, scoped to the declaration's value expression.

| Parameter | Description |
|---|---|
| `file` | Target Go file path |
| `identifier` | Name of the const or var |
| `patches` | Ordered list of patch operations (same shape as patch_function) |
| `preview` | Return the diff without writing |

#### `insert_call` tool

Insert a single statement into a function body — use this instead of `update` when you only need to add one line.

| Parameter | Required | Description |
|---|---|---|
| `file` | yes | Target Go file |
| `function` | yes | Function identifier (`FuncName` or `Receiver.Method`) |
| `call` | yes | Statement to insert, e.g. `setupPayOrderRoute(mux, app)` |
| `position` | no | `before-return` (default), `end-of-body`, or `after:<marker>` |

Idempotent: if the exact call already exists in the body, it is skipped with a warning.

#### Interface tools

**`add_interface`**, **`update_interface`**, **`delete_interface`** — manage interfaces together with their mocks.

| Parameter | Description |
|---|---|
| `file` | File containing the interface (required) |
| `identifier` | Interface name (required for update/delete) |
| `content` | Raw Go interface source (add/update) |
| `mock_file` | Target file for the generated mock (add/update), or file containing the mock to delete (delete) |
| `mock_name` | Name of the mock struct |
| `doc` / `strip_doc` | Doc comment handling (update only) |
| `delete_mock` | (delete only) also remove the mock struct, its methods, and the compile-time assertion from `mock_file`; requires `mock_file` and `mock_name` |

`add_interface` and `update_interface` regenerate the mock atomically when both `mock_file` and `mock_name` are set.

**`delete_interface` with `delete_mock=true`** cleans up the mock in the same call — the mock file itself is kept intact even if it becomes empty, so other mocks sharing the file are not disturbed. Without `delete_mock`, the compile-time assertion `var _ I = (*MockI)(nil)` will break `go build`, forcing explicit cleanup.

**Granular edits:** to add, rename, remove, or retype a single method, prefer [`patch_interface`](#patch_interface-tool) over `update_interface` — it avoids re-sending the whole declaration and regenerates the mock the same way.

#### `implement` tool

Generate missing method stubs on a struct for any interface.

| Parameter | Description |
|---|---|
| `interface` | Fully qualified interface name, e.g. `io.ReadCloser` or `github.com/org/repo/pkg.Interface` |
| `receiver` | Receiver type, e.g. `*MyStruct` |
| `file` | Target file to append stubs to |

Resolves the interface via `go/packages`. Scans the package to avoid cross-file duplicates. Generated stubs contain `// TODO: implement` and `panic("not implemented")`.

Use for interfaces you **don't own**. For interfaces you own, prefer `add_interface` (which creates the mock too).

#### `mock` tool

Generate a standalone mock for any interface without modifying the interface file.

| Parameter | Description |
|---|---|
| `interface` | Fully qualified interface name |
| `mock_name` | Name of the mock struct |
| `file` | Target file to write the mock to |

Uses the same function-field pattern as `add_interface`. Use for third-party interfaces.

#### `extract_interface` tool

Extract an interface from an existing struct's exported methods.

| Parameter | Description |
|---|---|
| `file` | File containing the struct |
| `identifier` | Struct identifier |
| `name` | Name of the interface to create |
| `out` | Output file path for the interface (optional) |
| `mock_file` / `mock_name` | Optional mock generation |

#### `test` tool

Generate a table-driven test skeleton.

| Parameter | Description |
|---|---|
| `file` | Target Go file containing the function |
| `identifier` | Function or method identifier |

Creates a `_test.go` file next to the source file.

#### `tag` tool

Add or update struct field tags.

| Parameter | Description |
|---|---|
| `file` | File containing the struct |
| `identifier` | Struct name |
| `field` | Specific field name (use with `set`) |
| `set` | Exact tag string to set or append |
| `auto` | Auto-generate tags for all exported fields (`json`, `bson`, …) |

#### `execute_plan` tool

Run up to 15 edits atomically from a YAML plan.

| Parameter | Description |
|---|---|
| `plan` | YAML plan content |

See [Batch plans: `execute`](#batch-plans-execute) below for the YAML schema.

`execute_plan` accepts every individual edit action as an action type, including the in-place `patch_function`, `patch_struct`, `patch_interface`, `patch_file`, and `patch_decl`. Each action carries its own per-type `patch_*_ops` slice so batched in-place edits stay type-checkable.

#### `build_check` tool

Run `go build` scoped to a package/directory and return structured compile diagnostics.

| Parameter | Description |
|---|---|
| `dir` | Relative directory or package pattern; defaults to `./...` |
| `affected_by` | Path to a `.go` file — narrow the build to that file's owning package plus every in-module package that (transitively) imports it. Mutually exclusive with `dir`. |
| `tests` | Also compile `_test.go` files |
| `timeout_seconds` | Timeout, default 60, max 600 |

Diagnostics come out as `{file, line, column, message}` deduplicated per file. On a large monorepo, `affected_by` is often an order of magnitude faster than `./...`.

#### `test_run` tool

Run `go test` scoped to a package/directory and return a compact pass/fail report.

| Parameter | Description |
|---|---|
| `dir` | Directory to test; defaults to `./...` |
| `affected_by` | Path to a `.go` file — same reverse-dep narrowing as `build_check`. Mutually exclusive with `dir`. |
| `run` | Optional `-run` regexp filter |
| `count`, `race`, `tags` | Passed through to `go test` |
| `timeout_seconds` | Timeout, default 120, max 600 |

#### `batch_query` tool

Run up to 10 read-only queries in a single round-trip. Useful when exploration would otherwise be N sequential calls.

| Parameter | Description |
|---|---|
| `queries` | Array of sub-queries; each has `op` (`symbol` / `overview` / `find_references` / `find_definition`) plus that op's normal parameters |

Fail-soft: if one sub-query errors, the others still return. Shares the project's packages loader cache — N `find_references` calls in one batch cost roughly one `packages.Load`.

#### `describe_tool` tool

Queryable catalog of every go-surgeon tool. Prefer this over reading the full server instructions blob when you just need "what should I use for X".

| Parameter | Description |
|---|---|
| `name` | Single tool detail (summary, example, related tools). Mutually exclusive with `category`. |
| `category` | Filter to one group: `explore`, `refs`, `edit`, `interface`, `codegen`, `validate`, `batch`, `meta` |

With no args, returns the full grouped list.

#### Error shape

Every tool's error response includes a structured `{code, message}` in `StructuredContent` alongside the human-readable text. Codes agents can branch on include:

- `INVALID_ARGUMENT` — malformed input (missing required field, mutually-exclusive params both set)
- `NOT_FOUND` — symbol / file / package doesn't exist
- `CONFLICT` — rename would collide with an existing name in scope
- `PATCH_FAILED` — match ambiguous, zero-match, or patch validation failed
- `PATCH_PRODUCES_INVALID_GO` — the rewrite parsed/compiled wrong; file on disk untouched
- `LOAD_ERROR` / `READ_ERROR` / `WRITE_ERROR` — I/O-level failures
- `INTERNAL` — tool bug; report it
- `ERROR` — validation errors that predate structured codes
- `UNKNOWN` — error from a dependency that didn't carry a code

---

## CLI reference

### Global flags

| Flag | Description |
|---|---|
| `--dry-run` | Preview changes as unified diff instead of writing to disk |
| `--diff` | Alias for `--dry-run` |

All subcommands support these flags.

### Exploration: `graph`, `symbol`

#### `graph`

Walk all Go packages and print their import paths.

```
go-surgeon graph [flags]
```

| Flag | Short | Default | Description |
|---|---|---|---|
| `--symbols` | `-s` | false | Include exported symbols per file |
| `--summary` | `-S` | false | Append package doc comment summary |
| `--deps` | `-D` | false | Show internal package import dependencies |
| `--recursive` | `-r` | false | Walk sub-packages when `--symbols` is set |
| `--tests` | `-t` | false | Include `_test.go` files |
| `--dir` | `-d` | `.` | Directory to walk |
| `--depth` | | 0 | Limit directory recursion depth (0 = unlimited) |
| `--focus` | | | Package path for full detail; others show path only |
| `--exclude` | | | Glob patterns to skip (repeatable) |
| `--token-budget` | | 0 | Approximate max tokens in output |
| `--module` | | | Import path of a dependency to explore |

`--symbols` requires `--dir`, `--focus`, or `--module` to scope the output.

**Examples:**

```bash
# List all packages
go-surgeon graph

# Symbols in one package (non-recursive)
go-surgeon graph --symbols --dir internal/catalog/domain

# Include test files
go-surgeon graph --symbols --tests --dir internal/catalog/app/commands

# Full architectural overview
go-surgeon graph --summary --deps --symbols --dir internal/catalog

# Focus on a single package with full detail
go-surgeon graph --summary --symbols --focus internal/catalog/domain

# Fit output within ~2000 tokens
go-surgeon graph --summary --deps --token-budget 2000

# Explore a dependency's package structure
go-surgeon graph --module github.com/spf13/cobra

# Symbols in a dependency sub-package
go-surgeon graph --symbols --module github.com/spf13/cobra --dir doc
```

#### `symbol`

Look up a function, method, or struct by name.

```
go-surgeon symbol <[Receiver.]Name> [flags]
```

| Flag | Short | Default | Description |
|---|---|---|---|
| `--body` | `-b` | false | Show the full function/struct body |
| `--tests` | `-t` | false | Include `_test.go` files |
| `--dir` | `-d` | `.` | Directory to search in |
| `--module` | | | Search in a dependency instead of the current project |

**Query forms:**

- `Name` — any function or struct named `Name`
- `Receiver.Method` — method `Method` on receiver `Receiver`
- `pkg.Name` — package-qualified

**Examples:**

```bash
# Find a symbol
go-surgeon symbol NewBook

# Method lookup with full body
go-surgeon symbol BookHandler.Handle --body

# Scope to a directory
go-surgeon symbol Validate --dir internal/catalog/domain

# Look up a symbol inside a dependency
go-surgeon symbol Command.Execute --body --module github.com/spf13/cobra
```

If the query matches multiple symbols, a disambiguation index is returned. Refine with `Receiver.Method` or `--dir`.

#### `find-definition`, `find-references`, `rename-symbol`

Type-aware lookup and rename. All three resolve the target via `go/packages`, so they ignore same-named identifiers in unrelated packages.

```
go-surgeon find-definition <NAME> [--receiver R] [--package P] [--file F --line N] [--dir D] [--tests]
go-surgeon find-references <NAME> [--include-definition] [--receiver R] [--package P] [--dir D] [--tests]
go-surgeon rename-symbol <OLD> <NEW> [--receiver R] [--package P] [--dir D] [--tests] [--preview]
```

Disambiguators (`--receiver`, `--package`, `--file`+`--line`) are the same on all three. `rename-symbol --preview` lists every rewrite site without touching files. Rename refuses export-status flips (`Foo` → `foo`) and same-scope collisions.

**Examples:**

```bash
# Where is BookRepository defined?
go-surgeon find-definition BookRepository

# Show every call site, plus the declaration
go-surgeon find-references BookRepository --include-definition

# Dry-run a rename before committing
go-surgeon rename-symbol BookRepo BookRepository --preview

# Rename a method (disambiguate by receiver)
go-surgeon rename-symbol Handle Serve --receiver BookHandler
```

### Editing: `create-file`, `add-func`, `update-func`, …

The CLI exposes one subcommand per action. Raw Go source goes in via stdin; metadata via flags.

**Common flags:**

| Flag | Short | Required for | Description |
|---|---|---|---|
| `--file` | `-f` | all | Target file path |
| `--id` | `-i` | update/delete | AST identifier (`FuncName` or `Receiver.Method`) |
| `--doc` | | update | Set or replace the doc comment |
| `--strip-doc` | | update | Remove the existing doc comment |
| `--with-test` | | add-func / update-func | Generate a test skeleton alongside |

#### Files

```bash
# Create (must not exist)
cat <<'EOF' | go-surgeon create-file --file internal/catalog/domain/book.go
package domain

type Book struct {
    ID    string
    Title string
}
EOF

# Replace (must exist)
cat <<'EOF' | go-surgeon replace-file --file internal/catalog/domain/book.go
package domain

type Book struct {
    ID        string
    Title     string
    CreatedAt time.Time
}
EOF
```

#### Functions

```bash
# Add
cat <<'EOF' | go-surgeon add-func --file internal/catalog/domain/book.go
func NewBook(title string) (*Book, error) {
    if title == "" {
        return nil, errors.New("title required")
    }
    return &Book{Title: title}, nil
}
EOF

# Update
cat <<'EOF' | go-surgeon update-func --file internal/catalog/domain/book.go --id NewBook
func NewBook(title, author string) (*Book, error) {
    return &Book{Title: title, Author: author}, nil
}
EOF

# Update a method (Receiver.Method form, short flags)
cat <<'EOF' | go-surgeon update-func -f internal/catalog/domain/book.go -i Book.Validate
func (b *Book) Validate() error {
    return nil
}
EOF

# Delete (no stdin)
go-surgeon delete-func --file internal/catalog/domain/book.go --id NewBook
go-surgeon delete-func -f internal/catalog/domain/book.go -i Book.Validate
```

#### Structs

```bash
cat <<'EOF' | go-surgeon add-struct --file internal/catalog/domain/book.go
type BookStatus string

const (
    BookStatusDraft     BookStatus = "draft"
    BookStatusPublished BookStatus = "published"
)
EOF

cat <<'EOF' | go-surgeon update-struct --file internal/catalog/domain/book.go --id Book
type Book struct {
    ID        string
    Title     string
    Author    string
    Status    BookStatus
    CreatedAt time.Time
}
EOF

# delete-struct also removes every method on the struct
go-surgeon delete-struct --file internal/catalog/domain/book.go --id Book
```

### Targeted inserts: `insert-call`

Insert a single statement into a function body at a controlled position.

```
go-surgeon insert-call --file <path> --id <function> --call <statement> [--position <pos>]
```

| Flag | Required | Default | Description |
|---|---|---|---|
| `--file` | yes | | Target Go file |
| `--id` | yes | | Function identifier (`FuncName` or `Receiver.Method`) |
| `--call` | yes | | Statement to insert |
| `--position` | no | `before-return` | `before-return`, `end-of-body`, or `after:<marker>` |

**Examples:**

```bash
# Before the function's return statement (default)
go-surgeon insert-call \
  --file internal/catalog/app/init.go \
  --id NewApp \
  --call "handlers.RegisterBookRoute(mux, app)"

# End of function body
go-surgeon insert-call -f internal/catalog/app/init.go -i NewApp \
  --call "logger.Info(\"app initialized\")" \
  --position end-of-body

# After a marker comment
go-surgeon insert-call -f internal/catalog/app/init.go -i NewApp \
  --call "setupPayOrderRoute(mux, app)" \
  --position "after:// routes"
```

**Idempotent:** if the exact statement already exists in the body, it is skipped with a warning.

### Interfaces and mocks: `add-interface`, `implement`, `mock`, `extract-interface`

#### `add-interface`, `update-interface`, `delete-interface`

Manage interfaces together with their function-field mocks.

| Flag | Short | Required for | Description |
|---|---|---|---|
| `--file` | `-f` | all | File containing the interface |
| `--id` | `-i` | update/delete | Interface name |
| `--mock-file` | `-m` | add/update | Target file for the generated mock |
| `--mock-name` | `-n` | add/update | Name of the mock struct |
| `--doc` | | update | Set or replace the doc comment |
| `--strip-doc` | | update | Remove the existing doc comment |

**Add an interface with auto-generated mock:**

```bash
cat <<'EOF' | go-surgeon add-interface \
  --file internal/catalog/domain/repositories/book/book.go \
  --mock-file internal/catalog/domain/repositories/book/booktest/mock.go \
  --mock-name MockBookRepository
type BookRepository interface {
    Create(ctx context.Context, book domain.Book) error
    FindByID(ctx context.Context, id BookID) (*domain.Book, error)
}
EOF
```

**Update an interface and regenerate its mock:**

```bash
cat <<'EOF' | go-surgeon update-interface \
  --file internal/catalog/domain/repositories/book/book.go \
  --id BookRepository \
  --mock-file internal/catalog/domain/repositories/book/booktest/mock.go \
  --mock-name MockBookRepository
type BookRepository interface {
    Create(ctx context.Context, book domain.Book) error
    FindByID(ctx context.Context, id BookID) (*domain.Book, error)
    Delete(ctx context.Context, id BookID) error
}
EOF
```

**Delete an interface:**

```bash
go-surgeon delete-interface --file internal/catalog/domain/repositories/book/book.go --id BookRepository
```

The CLI does **not** auto-delete the mock. The compile-time assertion `var _ BookRepository = (*MockBookRepository)(nil)` will break `go build`, forcing explicit cleanup of the mock and any dependent tests. (The MCP server exposes a `delete_mock=true` parameter that handles this automatically — see [`delete_interface`](#interface-tools) in the MCP reference.)

**Generated mock pattern:**

```go
type MockBookRepository struct {
    CreateFunc   func(ctx context.Context, book domain.Book) error
    FindByIDFunc func(ctx context.Context, id BookID) (*domain.Book, error)
}

func (m *MockBookRepository) Create(ctx context.Context, book domain.Book) error {
    if m.CreateFunc == nil {
        panic("MockBookRepository.CreateFunc not set")
    }
    return m.CreateFunc(ctx, book)
}

var _ book.BookRepository = (*MockBookRepository)(nil)
```

#### `implement`

Generate missing method stubs on a struct for any interface.

```
go-surgeon implement <package.Interface> --receiver <type> --file <path>
```

| Flag | Short | Required | Description |
|---|---|---|---|
| `--receiver` | `-r` | yes | Receiver type, e.g. `*MyStruct` |
| `--file` | `-f` | yes | Target file to append stubs to |

**Examples:**

```bash
# Implement a stdlib interface
go-surgeon implement io.ReadCloser --receiver "*MyReader" --file internal/pkg/reader.go

# Implement a project-local interface (full import path)
go-surgeon implement github.com/myorg/myapp/domain.Repository \
  --receiver "*pgRepository" \
  --file internal/outbound/pg/pg_repo.go
```

Stubs contain `// TODO: implement` and `panic("not implemented")`. Use for interfaces you **don't own**.

#### `mock`

Generate a standalone mock for any interface.

```
go-surgeon mock <package.Interface> --mock-name <name> --file <path>
```

| Flag | Short | Required | Description |
|---|---|---|---|
| `--mock-name` | `-m` | yes | Mock struct name |
| `--file` | `-f` | yes | Target file to write the mock to |

```bash
go-surgeon mock io.ReadCloser --mock-name MockReadCloser --file internal/mocks/readcloser.go

go-surgeon mock github.com/myorg/myapp/domain.Repository \
  --mock-name MockRepository \
  --file internal/mocks/mock_repository.go
```

#### `extract-interface`

Extract an interface from an existing struct's exported methods.

```
go-surgeon extract-interface --file <path> --id <struct> --name <interface> [flags]
```

| Flag | Short | Required | Description |
|---|---|---|---|
| `--file` | `-f` | yes | File containing the struct |
| `--id` | `-i` | yes | Struct identifier |
| `--name` | `-n` | yes | Name of the interface to create |
| `--out` | `-o` | no | Output file path for the interface |
| `--mock-file` | `-m` | no | Generate mock file path |
| `--mock-name` | | no | Name of the mock struct |

```bash
go-surgeon extract-interface \
  --file internal/outbound/pg/pg_book.go \
  --id pgBookRepository \
  --name BookRepository \
  --out internal/domain/repositories/book/book.go \
  --mock-file internal/domain/repositories/book/booktest/mock.go \
  --mock-name MockBookRepository
```

### Code generation: `test`, `tag`

#### `test`

Generate a table-driven test skeleton for a function or method.

```
go-surgeon test --file <path> --id <function>
```

| Flag | Short | Required | Description |
|---|---|---|---|
| `--file` | `-f` | yes | Target Go file containing the function |
| `--id` | `-i` | yes | Function or method identifier |

```bash
go-surgeon test --file internal/catalog/domain/book.go --id NewBook
go-surgeon test -f internal/catalog/domain/book.go -i Book.Validate
```

The `_test.go` file is created next to the source file.

#### `tag`

Add or update struct field tags.

```
go-surgeon tag --file <path> --id <struct> [--field <f> --set <tag>] [--auto <format>]
```

| Flag | Short | Required | Description |
|---|---|---|---|
| `--file` | `-f` | yes | File containing the struct |
| `--id` | `-i` | yes | Struct identifier |
| `--field` | | no | Specific field name to update (use with `--set`) |
| `--set` | | no | Exact tag string to set/append |
| `--auto` | | no | Auto-generate tags for exported fields (`json`, `bson`) |

```bash
# Auto-generate json tags for all exported fields
go-surgeon tag --file internal/catalog/domain/book.go --id Book --auto json

# Set a specific tag on one field
go-surgeon tag --file internal/catalog/domain/book.go --id Book \
  --field Title --set 'json:"title,omitempty" validate:"required"'
```

### Batch plans: `execute`

Run multiple edits from a YAML plan file.

```
go-surgeon execute [plan.yaml]
go-surgeon execute --file plan1.yaml --file plan2.yaml
cat plan.yaml | go-surgeon execute
```

| Flag | Short | Description |
|---|---|---|
| `--file` | `-f` | Plan YAML file to execute (repeatable; auto-cleaned on success) |
| `--keep` | `-k` | Retain plan files even on success (only with `--file`) |

**YAML schema:**

```yaml
actions:
  - action: create_file      # or replace_file, add_func, update_func, delete_func,
                             #    add_struct, update_struct, delete_struct,
                             #    add_interface, update_interface, delete_interface,
                             #    insert_call
    file: path/to/file.go
    identifier: FuncName     # or Receiver.Method (for update/delete)
    content: |
      func NewBook(title string) (*Book, error) {
          return &Book{Title: title}, nil
      }
    mock_file: path/to/mock.go       # for add_interface / update_interface
    mock_name: MockBookRepository    # for add_interface / update_interface
    position: before-return          # for insert_call
```

**Example — a multi-step refactor as one atomic operation:**

```yaml
actions:
  - action: update_struct
    file: internal/catalog/domain/book.go
    identifier: Book
    content: |
      type Book struct {
          ID        string
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
          return &Book{Title: title, Author: author, Status: status}, nil
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

Run it:

```bash
go-surgeon execute refactor.yaml
```

All actions execute in order. If any fails, the error identifies which action and why.

---

## Core rules

These apply to every edit, on CLI and MCP:

1. **Content is raw Go source.** Never include a `package` declaration or `import` block — `goimports` runs automatically after every mutation.
2. **`update-*` needs the complete declaration.** Include the full signature.
3. **Never manage imports manually.** `goimports` handles all import changes.
4. **Never worry about indentation.** `goimports` reformats everything.
5. **Each command is atomic.** Clear success message, or a structured error:
   ```
   ERROR (update-func): node 'Book.Validate' not found in internal/catalog/domain/book.go
   Hint: use 'go-surgeon symbol Book.Validate' to locate it.
   ```
6. **Use `--dry-run` (CLI) or `preview=true` (MCP `patch_*`) to preview.** No changes written to disk; a unified diff is printed instead.

---

## FAQ

### My pre-commit hook rejects a patch containing a URL or credential-like string

Some pre-commit secret-scanner hooks (e.g. `detect-secrets`, `gitleaks`) trigger on strings like `postgres://user:password@host` even inside patch operations. This is a false positive in the hook, not a go-surgeon bug.

Options:
1. Adjust your hook's allowlist for the affected file or pattern.
2. Rephrase the patch to avoid the sensitive-looking substring in the `match` field — use `match_regex` with a pattern that avoids the literal string, or use `at_line`/`from_line`/`to_line` targeting instead of text matching.
3. Use `patch target=file` with `match_regex` and capture groups to avoid reproducing the full credential string in the match.

---

## See also

- [`README.md`](README.md) — overview and positioning
- [`examples/`](examples) — real editing sessions
