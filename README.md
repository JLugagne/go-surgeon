# go-surgeon: The AST-Based Code Editor for Go

## Motivation

AI agents (LLMs) currently waste massive amounts of time and tokens trying to modify existing Go code using generic text replacement tools. These methods are inherently fragile, prone to indentation hallucinations, and often result in loops of syntax errors.

**go-surgeon** solves this by providing a deterministic CLI that uses Go's Abstract Syntax Tree (go/ast) to locate code and a byte-range replacement engine to modify it with surgical precision.

**Key Benefits:**
- **Zero Indentation Errors:** We locate the exact byte offsets and let `goimports` handle the formatting and imports automatically.
- **Perfect Context Preservation:** Unlike standard AST mutation, our byte-range engine preserves all internal comments and allows seamless updates to Godoc (documentation) blocks.
- **Turn Efficiency:** Drastically reduces the number of conversational turns needed for LLMs to explore a codebase and perform complex refactors.
- **Smart Navigation:** Acts as a CLI-based LSP, allowing AI to query function signatures and bodies without loading entire files into context.
- **Architecture Enforcement:** Deterministic scaffolding ensures AI agents follow established architectural patterns instead of improvising.

## Core Features

### 1. Package & Symbol Graph (`graph`)
Walk all Go packages and print their import paths. With `--symbols --dir`, list every exported type, function, and method in a subtree — a structural map in one command.

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

### 7. Deterministic Scaffolding (`scaffold`)
Generates standard architecture components from local templates, providing immediate structural scaffolding without LLM hallucination.

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

# List scaffolding templates and generate code
go-surgeon scaffold
go-surgeon scaffold catalog --name orders --module github.com/myorg/myapp
```

See `USAGE.md` for detailed documentation on all commands and flags.
