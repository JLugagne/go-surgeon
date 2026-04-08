# Feature: Package-Qualified Symbol Lookup

## Motivation
Currently, `go-surgeon symbol` accepts standard names (`MyFunc`) or receiver-qualified names (`Receiver.Method`). However, in large projects, developers often think in terms of package namespaces, and function names might conflict across packages (e.g., multiple `New()` or `Handle()` functions).

## Design
Enhance the `identifier` parsing in the `symbol` command (and potentially mutation commands) to support the standard Go package-qualified format: `package.Struct` or `package.Func`.

## Behavior
1. **Query format:** `go-surgeon symbol mypkg.MyFunc` or `go-surgeon symbol mypkg.MyStruct.MyMethod`
2. **Resolution:**
   - The tool must map `mypkg` to a specific directory in the project graph.
   - It filters the AST search to only look within files that declare `package mypkg`.
   - If the package path is ambiguous, it should resolve it using the module's dependency graph or require the `--dir` flag as a root to search from.
3. This syntax should ideally be supported across all commands where an `--id` is used, acting as an implicit directory filter to ensure the correct symbol is targeted even if the command is run from the project root.

## Benefits
- Aligns perfectly with how Go developers naturally read and write code.
- Drastically reduces false positives when searching for common names like `Config`, `New`, or `Run`.
