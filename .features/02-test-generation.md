# Feature: Table-Driven Test Generation

## Motivation
Writing table-driven tests is a standard practice in Go, but it is repetitive and token-heavy for LLMs. LLMs often make mistakes in the structure of the table or the type definitions of the test cases.

## Design
Add a new command: `go-surgeon test --file <path> --id <FuncName>`

## Behavior
1. The tool parses the target file and extracts the exact signature of the specified function or method (inputs, outputs, receiver if any).
2. It generates a robust, idiomatic Go table-driven test skeleton in the corresponding `_test.go` file.
3. The generated test should include:
   - A `tests` slice of structs with `name`, `args` (matching input params), `want` (matching outputs), and `wantErr bool` (if it returns an error).
   - A `t.Run` loop iterating over the test cases.
   - `assert` or `require` statements for basic validation.
4. The command outputs the location of the generated test, allowing the LLM to simply use `update-func` on the test function to fill in the actual test cases.

## Benefits
- Enforces idiomatic testing patterns automatically.
- Saves massive amounts of generation time and tokens for AI agents.
