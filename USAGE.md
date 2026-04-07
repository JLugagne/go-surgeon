# go-surgeon Usage Guide

This document details all available commands, their flags, and how to use them effectively.

## 1. Symbol Exploration (`symbol`)

The `symbol` command acts as a lightweight, CLI-based Language Server for LLMs. It searches the AST to find specific functions, methods, or structs and returns their signatures, documentation, and line numbers.

**Usage:**
```bash
go-surgeon symbol <[Receiver.]Name> [--body] [--dir <path>]
```

**Examples:**
- `go-surgeon symbol FreeFunc` : Finds a global function or struct named `FreeFunc`.
- `go-surgeon symbol MyStruct.DoWork` : Finds the `DoWork` method attached to `MyStruct`.
- `go-surgeon symbol DoWork --body` : Returns the full source code of the method, stripping empty lines to save tokens while preserving original line numbers.

**Output:**
If multiple matches are found, it returns a dense index to help refine the search. If an exact match is found, it provides detailed location data and the code.

## 2. Interface Implementation (`implement`)

The `implement` command automatically generates missing methods to satisfy a specific Go interface. It scans the entire target directory to prevent duplicating methods that might exist in sibling files, and validates the signatures of existing methods to prevent conflicts.

**Usage:**
```bash
go-surgeon implement <package.Interface> --receiver <Receiver> --file <File>
```

**Examples:**
- `go-surgeon implement context.Context --receiver "*MyContext" --file internal/pkg/my_context.go`

**Behavior:**
- Generates `// TODO: implement <MethodName>` and `panic("not implemented")` stubs for missing methods.
- Automatically formats the file and adds required imports.
- Returns a summary of the generated methods in the same format as the `symbol` command.

## 3. Surgical Editing (`execute`)

The `execute` command applies a YAML plan of AST modifications. This prevents syntax and indentation errors common with standard regex/sed replacements.

**Usage:**
```bash
go-surgeon execute <plan.yaml>
cat plan.yaml | go-surgeon execute
```

**YAML Plan Structure:**
```yaml
actions:
  - action: add_func         # Available actions below
    file_path: main.go
    identifier: MyFunc       # Required for update/delete operations
    content: |               # The raw Go code to insert
      func MyFunc() {
          fmt.Println("Hello")
      }
```

**Supported Actions:**
- `create_file`: Creates a new file and parent directories.
- `replace_file`: Completely overwrites a file.
- `add_func` / `add_struct`: Appends a new function or struct to the file.
- `update_func` / `update_struct`: Surgically replaces an existing function body or struct definition. If the new `content` includes Godoc comments (`//` above the node), it will replace the old documentation block entirely.
- `delete_func`: Removes a function and its associated documentation.
- `delete_struct`: Cascading delete. Removes the struct definition, its documentation, **and all methods** (receivers) attached to that struct within the file.

*Note: Our engine uses precise byte-range replacement. This ensures that only the targeted block is modified, leaving the rest of the file (and its comments) untouched.*

## 4. Scaffolding (`scaffold` & `list`)

These commands use your local `.templates/` directory to generate boilerplate architecture without relying on LLM generation.

**Usage:**
```bash
# List available templates and their parameters
go-surgeon list

# Scaffold a template (e.g., 'app' template)
go-surgeon scaffold app --domain catalog

# Scaffold a sub-template (e.g., 'feature/command')
go-surgeon scaffold feature/command --name CreateBook --domain catalog
```
