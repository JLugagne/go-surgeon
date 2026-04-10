# go-surgeon Usage Guide

This document details all available commands, their flags, and how to use them effectively.

All flags use the standard `--long-name` form. Single-character short aliases are available for every flag (shown in parentheses).

---

## 1. Package Graph (`graph`)

Walks all Go packages and prints their import paths. The fastest way to orient in an unfamiliar codebase. Context window management flags let agents progressively zoom in without overwhelming their token budget.

```bash
go-surgeon graph [--symbols] [--dir <path>] [--depth N] [--focus <path>] [--exclude <glob>] [--token-budget N]
```

### Core flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--symbols` | `-s` | false | Include exported symbols per file |
| `--summary` | `-S` | false | Append package doc comment summary |
| `--deps` | `-D` | false | Show internal import dependencies |
| `--recursive` | `-r` | false | Walk sub-packages when `--symbols` is set |
| `--tests` | `-t` | false | Include `_test.go` files (shows unexported helpers) |
| `--dir` | `-d` | `.` | Directory to walk (relative to module root when `--module` is set) |
| `--module` | | (none) | Import path of a dependency to explore instead of the current project |

`--symbols` requires `--dir`, `--focus`, or `--module` to prevent overwhelming output on large projects.

### Context window management flags

| Flag | Default | Description |
|------|---------|-------------|
| `--depth` | `0` (unlimited) | Limit directory recursion depth. `1` = target dir only, `2` = immediate children. |
| `--focus` | (none) | Package path for full detail (symbols + summary); all other packages show path only. Implies `--symbols --summary -r`. Relative to module root when `--module` is set. |
| `--exclude` | (none) | Glob pattern for directories to skip. Repeatable (e.g. `--exclude vendor --exclude "*legacy*"`). |
| `--token-budget` | `0` (unlimited) | Approximate max tokens in output. Progressively truncates: summaries → deps → symbols → files → package list. |

### Examples

```bash
# List all packages
go-surgeon graph

# List exported symbols in a subtree
go-surgeon graph --symbols --dir internal/catalog/domain

# Short flags
go-surgeon graph -s -d internal/catalog/domain

# High-level overview with summaries and dependency graph
go-surgeon graph --summary --deps

# Limit depth to 2 directory levels
go-surgeon graph --summary --depth 2

# Zoom into a single package with full detail, path-only for the rest
go-surgeon graph --focus internal/catalog/domain

# Exclude vendor and legacy directories
go-surgeon graph --exclude vendor --exclude "*legacy*"

# Fit output within ~2000 tokens (progressive truncation)
go-surgeon graph --summary --deps --token-budget 2000

# Explore a dependency's package structure (version resolved from go.mod)
go-surgeon graph --module github.com/spf13/cobra

# Symbols in a specific sub-package of a dependency
go-surgeon graph --symbols --module github.com/spf13/cobra --dir doc

# Focus on one package within a dependency
go-surgeon graph --module github.com/spf13/cobra --focus cobra
```

### Progressive discovery strategy

For large codebases, use the context management flags to adopt a zoom-in workflow:

1. **High-level map:** `go-surgeon graph --summary --depth 2` — see top-level packages and their descriptions.
2. **Zoom in:** `go-surgeon graph --focus internal/catalog/domain` — full symbols for the target package, path-only for the rest.
3. **Deep dive:** `go-surgeon graph -s -d internal/catalog/domain` — detailed symbols in one subtree.

### Output examples

**Default (package paths only):**
```
internal/catalog/domain
internal/catalog/domain/repositories/book
internal/catalog/app/commands
internal/catalog/inbound/http
```

**With `--symbols`:**
```
internal/catalog/domain/book.go
  type Book struct { ID BookID; Title string; Author string }
  type BookID string
  func NewBook(title, author string) (*Book, error)

internal/catalog/domain/repositories/book/book.go
  type BookRepository interface { Create; FindByID; Delete }
```

**With `--focus internal/catalog/domain`:**
```
internal/catalog/app/commands
internal/catalog/inbound/http

internal/catalog/domain/book.go
  type Book struct { ID BookID; Title string; Author string }
  type BookID string
  func NewBook(title, author string) (*Book, error)

internal/catalog/domain/repositories/book/book.go
  type BookRepository interface { Create; FindByID; Delete }
```

---

## 2. Symbol Exploration (`symbol`)

Searches all Go files under `--dir` for a function, method, or struct matching the query. Acts as a lightweight CLI-based LSP.

```bash
go-surgeon symbol <[Receiver.]Name> [--body] [--dir <path>] [--module <importpath>]
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--body` | `-b` | false | Show the full source body |
| `--tests` | `-t` | false | Include `_test.go` files in the search |
| `--dir` | `-d` | `.` | Directory to search (relative to module root when `--module` is set) |
| `--module` | | (none) | Import path of a dependency to search instead of the current project |

**Query forms:**
- `Name` — matches any function or struct named `Name`
- `Receiver.Name` — matches method `Name` on receiver `Receiver`

**Examples:**
```bash
# Find a function or struct
go-surgeon symbol NewBook

# Find a specific method
go-surgeon symbol BookHandler.Handle

# Print the full body
go-surgeon symbol NewBook --body

# Scope to a directory
go-surgeon symbol Validate --dir internal/catalog/domain

# Short flags
go-surgeon symbol BookHandler.Handle -b -d internal/catalog

# Look up a symbol inside a dependency
go-surgeon symbol Command.Execute --module github.com/spf13/cobra --body

# Scope to a sub-package within a dependency
go-surgeon symbol GenMarkdown --module github.com/spf13/cobra --dir doc --body
```

**Output (exact match, no --body):**
```
Symbol: Handle
Receiver: BookHandler
File: internal/catalog/inbound/http/handler.go:22-45 (24 lines body)
Signature:
func (h *BookHandler) Handle(ctx context.Context, cmd CreateBookCommand) error
```

**Output (multiple matches):** A disambiguation index grouped by methods, functions, and structs. Refine with `Receiver.Method` or `--dir`.

---

## 3. Surgical Editing (per-action subcommands)

Each edit is its own subcommand. Raw Go source goes in via stdin; metadata via flags. `goimports` runs automatically after every mutation — do not include import statements.

### Common flags

| Flag | Short | Required for | Description |
|------|-------|-------------|-------------|
| `--file` | `-f` | all | Target file path |
| `--id` | `-i` | update/delete | AST identifier: `FuncName` or `Receiver.Method` |

### File-level commands

```bash
# Create a new file (must not exist)
cat <<'EOF' | go-surgeon create-file --file internal/catalog/domain/book.go
package domain

type Book struct {
    ID    BookID
    Title string
}
EOF

# Replace an entire file (must exist)
cat <<'EOF' | go-surgeon replace-file --file internal/catalog/domain/book.go
package domain

type Book struct {
    ID        BookID
    Title     string
    CreatedAt time.Time
}
EOF
```

### Function commands

```bash
# Append a function
cat <<'EOF' | go-surgeon add-func --file internal/catalog/domain/book.go
func NewBook(title string) (*Book, error) {
    if title == "" {
        return nil, errors.New("title required")
    }
    return &Book{ID: NewBookID(), Title: title}, nil
}
EOF

# Update a function (--id = FuncName or Receiver.Method)
cat <<'EOF' | go-surgeon update-func --file internal/catalog/domain/book.go --id NewBook
func NewBook(title, author string) (*Book, error) {
    if title == "" {
        return nil, errors.New("title required")
    }
    return &Book{ID: NewBookID(), Title: title, Author: author}, nil
}
EOF

# Update a method
cat <<'EOF' | go-surgeon update-func -f internal/catalog/domain/book.go -i Book.Validate
func (b *Book) Validate() error {
    return nil
}
EOF

# Delete a function (no stdin needed)
go-surgeon delete-func --file internal/catalog/domain/book.go --id NewBook
go-surgeon delete-func -f internal/catalog/domain/book.go -i Book.Validate
```

### Struct commands

Same pattern as function commands:

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
    ID        BookID
    Title     string
    Author    string
    Status    BookStatus
    CreatedAt time.Time
}
EOF

# delete-struct also removes all methods on the struct
go-surgeon delete-struct --file internal/catalog/domain/book.go --id Book
```

### Critical rules

1. **stdin = raw Go code.** No package statement, no imports. Just the declaration.
2. **`update-func/struct` needs the complete declaration** — include the full signature.
3. **Never manage imports.** `goimports` runs automatically on every mutation.
4. **Never worry about indentation.** `goimports` reformats everything.
5. **Each command is atomic** with a clear error: `ERROR (update-func): node 'Book.Validate' not found in ...`

---

## 4. Interface Management

Interfaces and their mocks are managed as a pair. `add-interface` and `update-interface` automatically generate (or regenerate) the mock.

### Flags

| Flag | Short | Required for | Description |
|------|-------|-------------|-------------|
| `--file` | `-f` | all | File containing the interface |
| `--id` | `-i` | update/delete | Interface name |
| `--mock-file` | `-m` | add/update | Target file for the generated mock |
| `--mock-name` | `-n` | add/update | Name of the mock struct |

### Add a new interface + mock

```bash
cat <<'EOF' | go-surgeon add-interface \
  --file internal/catalog/domain/repositories/book/book.go \
  --mock-file internal/catalog/domain/repositories/book/booktest/mock.go \
  --mock-name MockBookRepository
type BookRepository interface {
    Create(ctx context.Context, projectID types.ProjectID, book domain.Book) error
    FindByID(ctx context.Context, projectID types.ProjectID, id types.BookID) (*domain.Book, error)
}
EOF
```

### Update an interface + regenerate mock

```bash
cat <<'EOF' | go-surgeon update-interface \
  --file internal/catalog/domain/repositories/book/book.go \
  --id BookRepository \
  --mock-file internal/catalog/domain/repositories/book/booktest/mock.go \
  --mock-name MockBookRepository
type BookRepository interface {
    Create(ctx context.Context, projectID types.ProjectID, book domain.Book) error
    FindByID(ctx context.Context, projectID types.ProjectID, id types.BookID) (*domain.Book, error)
    Delete(ctx context.Context, projectID types.ProjectID, id types.BookID) error
}
EOF
```

### Delete an interface

```bash
go-surgeon delete-interface --file internal/catalog/domain/repositories/book/book.go --id BookRepository
```

Removes the interface only. The mock is **not** auto-deleted — `var _ BookRepository = (*MockBookRepository)(nil)` will break `go build`, forcing you to clean up the mock and dependent tests explicitly.

### Generated mock pattern

```go
type MockBookRepository struct {
    CreateFunc   func(ctx context.Context, projectID types.ProjectID, book domain.Book) error
    FindByIDFunc func(ctx context.Context, projectID types.ProjectID, id types.BookID) (*domain.Book, error)
}

func (m *MockBookRepository) Create(ctx context.Context, projectID types.ProjectID, book domain.Book) error {
    if m.CreateFunc == nil {
        panic("MockBookRepository.CreateFunc not set")
    }
    return m.CreateFunc(ctx, projectID, book)
}

var _ book.BookRepository = (*MockBookRepository)(nil)
```

---

## 5. Interface Implementation (`implement`)

Generates missing method stubs on a struct to satisfy any interface — stdlib, third-party, or project-local.

```bash
go-surgeon implement <package.Interface> --receiver <type> --file <path>
```

| Flag | Short | Description |
|------|-------|-------------|
| `--receiver` | `-r` | Receiver type, e.g. `*MyStruct` (required) |
| `--file` | `-f` | Target file to append stubs to (required) |

**Examples:**
```bash
go-surgeon implement io.ReadCloser --receiver "*MyReader" --file internal/pkg/reader.go
go-surgeon implement context.Context --receiver "*MyCtx" --file internal/ctx.go

# Short flags
go-surgeon implement io.Writer -r "*MyWriter" -f internal/pkg/writer.go
```

**Behavior:**
- Resolves the interface via `go/packages` (stdlib + third-party + project-local).
- Scans the entire package directory to avoid cross-file duplicates.
- Validates signature compatibility of existing methods.
- Generated stubs: `// TODO: implement` + `panic("not implemented")`.
- Returns a summary in the same format as `symbol`.

Use for interfaces you **don't own**. For interfaces you own, prefer `add-interface` which creates the mock too.

---

## 6. Standalone Mock Generation (`mock`)

Generates a function-field mock for any interface, including third-party.

```bash
go-surgeon mock <package.Interface> --mock-name <name> --file <path>
```

| Flag | Short | Description |
|------|-------|-------------|
| `--mock-name` | `-m` | Mock struct name, e.g. `MockBookRepository` (required) |
| `--file` | `-f` | Target file to write the mock to (required) |

**Examples:**
```bash
go-surgeon mock io.ReadCloser --mock-name MockReadCloser --file internal/mocks/readcloser.go

# Project-local interface (full import path)
go-surgeon mock github.com/myorg/myapp/domain.Repository \
  --mock-name MockRepository \
  --file internal/domain/repositorytest/mock.go

# Short flags
go-surgeon mock io.Writer -m MockWriter -f internal/mocks/writer.go
```

Same mock pattern as `add-interface`. Use for interfaces you **don't own**.

---

## 7. Batch Plan Execution (`execute`) — deprecated

> **Deprecated.** Use individual subcommands instead — they provide better error messages and are easier to script. `execute` will print a deprecation notice when used.

Reads a YAML plan file (or stdin) and executes all actions in order. No limit on the number of actions per plan.

```bash
go-surgeon execute plan.yaml
cat plan.yaml | go-surgeon execute
```

**YAML schema:**

| Field | Required | Description |
|-------|----------|-------------|
| `action` | always | `create_file`, `replace_file`, `add_func`, `update_func`, `delete_func`, `add_struct`, `update_struct`, `delete_struct`, `add_interface`, `update_interface`, `delete_interface` |
| `file` | always | Target file path |
| `identifier` | update/delete | `FuncName` or `Receiver.Method` |
| `content` | create/replace/add/update | Raw Go source (no package/imports) |
| `mock_file` | add/update_interface | Path for the generated mock file |
| `mock_name` | add/update_interface | Name of the generated mock struct |

---

## Dependency Exploration (`--module`)

Both `graph` and `symbol` accept `--module IMPORTPATH` to explore a third-party dependency's source instead of the current project. This is the right tool whenever you need to understand a library's actual API, read an implementation, or check behavior that isn't in the docs.

The version used is whatever is pinned in the project's `go.mod` — no version flag needed, and `replace` directives and vendoring are handled automatically.

```bash
# Package map of a dependency
go-surgeon graph --module github.com/spf13/cobra

# Symbols in the root package
go-surgeon graph --symbols --module github.com/spf13/cobra

# Scope to a sub-package using --dir (relative to module root)
go-surgeon graph --symbols --module github.com/spf13/cobra --dir doc

# Focus on one package, path-only for the rest
go-surgeon graph --module github.com/spf13/cobra --focus cobra

# Read a specific symbol
go-surgeon symbol Command --module github.com/spf13/cobra --body
go-surgeon symbol Command.Execute --module github.com/spf13/cobra --body

# Scope symbol search to a sub-package
go-surgeon symbol GenMarkdown --module github.com/spf13/cobra --dir doc --body
```

**Output:** All file paths are relative to the module root (no `@version` noise). A header line identifies the module and its resolved version:

```
# Module: github.com/spf13/cobra @ v1.10.2
# Location: /home/user/go/pkg/mod/github.com/spf13/cobra@v1.10.2

cobra.go
  type Command struct { ... }
  func (c *Command) Execute() error
  ...
doc/
  func GenMarkdown(cmd *Command, w io.Writer) error
  ...
```

**Error cases:**
- Module not in `go.mod`: clear error with a hint to check `go list -m all`.
- Module in `go.mod` but not downloaded: error suggests `go mod download`.
- `--dir` with an absolute path when `--module` is set: rejected (would escape the module root).

---

## Workflow Summary

### Orientation

```bash
go-surgeon graph                                      # packages map
go-surgeon graph --summary --depth 2                  # high-level overview
go-surgeon graph --focus internal/catalog/domain      # zoom into one package
go-surgeon graph --symbols --dir internal/catalog     # symbols in a subtree
go-surgeon graph --summary --deps --token-budget 2000 # fit within token budget
go-surgeon symbol BookHandler                         # find a specific symbol
go-surgeon symbol BookHandler.Handle --body           # read its body
```

### Before editing

```bash
# Find an existing pattern to follow
go-surgeon graph --symbols --dir internal/catalog/outbound
go-surgeon symbol PgBookRepo.Create --body

# Read what you're about to change
go-surgeon symbol BookHandler.Handle --body
```

### Editing

```bash
cat <<'EOF' | go-surgeon update-func --file internal/catalog/inbound/http/handler.go --id BookHandler.Handle
func (h *BookHandler) Handle(ctx context.Context, cmd CreateBookCommand) error {
    // new implementation
}
EOF
```

### Creating interfaces + mocks

```bash
cat <<'EOF' | go-surgeon add-interface \
  --file internal/catalog/domain/repositories/book/book.go \
  --mock-file internal/catalog/domain/repositories/book/booktest/mock.go \
  --mock-name MockBookRepository
type BookRepository interface {
    Create(ctx context.Context, projectID types.ProjectID, book domain.Book) error
}
EOF
```

### Implementing adapters

```bash
# Generate stubs
go-surgeon implement domain/repositories/book.BookRepository \
  --receiver "*pgBookRepository" \
  --file internal/catalog/outbound/pg/pg_book.go

# Fill each stub
cat <<'EOF' | go-surgeon update-func \
  --file internal/catalog/outbound/pg/pg_book.go \
  --id pgBookRepository.Create
func (r *pgBookRepository) Create(ctx context.Context, projectID types.ProjectID, book domain.Book) error {
    // implementation
}
EOF
```
