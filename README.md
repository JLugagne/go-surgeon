# go-surgeon: The AI-Native AST Code Editor for Go

AI agents and LLMs waste massive amounts of context window tokens and time trying to modify existing Go code using generic text replacement tools (like diff blocks or regex). These methods are inherently fragile, prone to indentation hallucinations, and often trap the AI in an endless loop of syntax errors.

**go-surgeon** solves this by providing a deterministic, CLI-first interface built on Go's Abstract Syntax Tree (go/ast). It acts as a lightweight Language Server and a byte-range replacement engine, empowering AI agents to read, explore, and modify code with absolute surgical precision.

## Why your AI agent needs go-surgeon

- **Zero Indentation Errors:** We locate the exact byte offsets in the AST. Your agent just streams the raw code block, and `goimports` handles formatting and imports automatically.
- **Perfect Context Preservation:** Standard AST mutations strip away internal comments. Our byte-range engine preserves all surrounding comments and seamlessly updates Godoc blocks.
- **Maximized Context Window:** Acts as a CLI-based LSP (`graph` and `symbol` commands). Agents can query function signatures, docs, and bodies without loading entire 2000-line files into their context window.
- **Drastic Turn Reduction:** Atomically update complex methods, interfaces, or structs in a single shot. No more "hunk matching" failures.
- **Workflow Orchestration:** The `scaffold` command provides a built-in workflow engine. Templates emit context-aware `hints` (Task Lists) that guide the AI step-by-step through building complex architectures (like Hexagonal/DDD) instead of letting it improvise.

## Core Features

### 1. Package & Symbol Graph (`graph`)
Walk all Go packages and print their import paths. With `--symbols --dir`, list every exported type, function, and method in a subtree — a structural map in one command. Context window management flags (`--depth`, `--focus`, `--exclude`, `--token-budget`) let agents progressively zoom in without overwhelming their token budget.

### 2. Code Exploration (`symbol`)
Query the AST to extract function signatures, documentation, or full bodies with empty lines stripped to save LLM tokens. Supports precise `Receiver.Method` lookups to cut through noise.

### 3. Surgical Editing (per-action subcommands)
Individual subcommands (`add-func`, `update-func`, `delete-func`, `add-struct`, etc.) each accept raw Go source on stdin and metadata via `--flags`. Every mutation runs `goimports` automatically.

### 4. Interface Management (`add-interface` / `update-interface` / `delete-interface`)
Create or update an interface and its function-field mock in one command. The mock is auto-generated and kept in sync.

### 5. Interface Implementation (`implement`)
Automatically generates missing method stubs on a struct to satisfy any interface — stdlib, third-party, or project-local. Scans the package to prevent cross-file duplicates.

### 6. Standalone Mock Generation (`mock`)
Generate a function-field mock for any interface you don't own without modifying the interface file.

### 7. Template-Driven Scaffolding (`scaffold`)
Generates standard architecture components from local templates. Features a built-in workflow engine with `post_commands` chaining and contextual `hints` to guide AI agents step-by-step through project construction.

## Quick Start

### Build
```bash
go build -o go-surgeon ./cmd/go-surgeon
```

### Shell completion (optional)
```bash
go-surgeon completion bash > /etc/bash_completion.d/go-surgeon   # bash
go-surgeon completion zsh > "${fpath[1]}/_go-surgeon"             # zsh
```

### Usage Overview
```bash
# Orient yourself — packages map, then symbols in a subtree
go-surgeon graph
go-surgeon graph --symbols --dir internal/catalog/domain

# Progressive discovery — zoom in without blowing up context
go-surgeon graph --summary --depth 2
go-surgeon graph --focus internal/catalog/domain
go-surgeon graph --summary --deps --token-budget 2000

# Read a symbol before editing it
go-surgeon symbol BookHandler.Handle --body

# Edit: pipe raw Go source, pass metadata as flags
cat <<'EOF' | go-surgeon update-func --file internal/catalog/domain/book.go --id NewBook
func NewBook(title, author string) (*Book, error) {
    return &Book{Title: title, Author: author}, nil
}
EOF

# Generate interface stubs on a struct
go-surgeon implement io.ReadCloser --receiver "*MyReader" --file internal/pkg/reader.go

# List scaffolding templates and read documentation
go-surgeon scaffold list-templates
go-surgeon scaffold doc hexagonal bootstrap

# Execute a scaffolding workflow
go-surgeon scaffold execute hexagonal bootstrap --set AppName=catalog
```

See `USAGE.md` for detailed documentation on all commands and flags, and `SCAFFOLDING.md` for instructions on creating templates.
