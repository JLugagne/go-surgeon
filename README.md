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

### 1. Code Exploration (`symbol`)
Query the AST to extract function signatures, documentation, or full bodies with empty lines stripped to save LLM tokens. It supports precise receiver-based lookups to cut through noise.

### 2. Surgical Editing (`execute`)
Accepts a YAML plan containing precise AST operations (add/update/delete for structs and functions). Modifies the AST, preserves surrounding comments, and automatically runs `goimports`.

### 3. Interface Implementation (`implement`)
Automatically generates missing methods for a target interface on a specific struct. It scans the entire package to prevent cross-file duplicates and validates existing signatures.

### 4. Deterministic Scaffolding (`scaffold` & `list`)
Generates standard architecture components (apps, CQRS features) from local templates, providing immediate structural scaffolding without LLM hallucination.

## Quick Start

### Build
```bash
go build -o go-surgeon ./cmd/go-surgeon
```

### Usage Overview
```bash
# Explore code
./go-surgeon symbol ExecutePlanHandler.deleteStruct --body

# Implement an interface
./go-surgeon implement context.Context --receiver "*MyStruct" --file my_file.go

# Execute an AST modification plan
./go-surgeon execute plan.yaml

# List scaffolding templates and generate code
./go-surgeon list
./go-surgeon scaffold app --domain catalog
```

See `USAGE.md` for detailed documentation on all commands and YAML structures.
