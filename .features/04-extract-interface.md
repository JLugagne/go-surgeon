# Feature: Interface Extraction

## Motivation
A common refactoring pattern in Go (especially in Hexagonal Architecture) is to extract an interface from an existing concrete implementation to invert dependencies. Asking an LLM to manually read the struct, copy all its method signatures, and format them into an interface is tedious and error-prone.

## Design
Add a new command: `go-surgeon extract-interface --file <path> --id <StructName> --name <InterfaceName> [--out <dest_path>]`

## Behavior
1. The tool locates the struct `<StructName>`.
2. It scans the AST (across the file or package) to find all exported methods (`FuncDecl` with a receiver matching the struct).
3. It collects the exact signatures of these exported methods.
4. It generates an interface block named `<InterfaceName>` containing all collected signatures.
5. If `--out` is provided, it creates/appends to that file; otherwise, it appends to the current `--file`.
6. (Optional) Could integrate with the existing mock generation so extracting an interface also generates its mock instantly.

## Benefits
- Automates a tedious structural refactoring step.
- Guarantees perfect signature matching between the concrete type and the new interface.
