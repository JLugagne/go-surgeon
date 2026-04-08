# Feature: Dry-Run and Diff Mode

## Motivation
Currently, `go-surgeon` modifies files directly in place. While this is efficient, developers and AI agents often want to preview the exact changes before committing them to the filesystem, especially during complex refactorings or when debugging a generated plan.

## Design
Add a global `--dry-run` (or `--diff`) flag to all mutation commands (`add-func`, `update-struct`, `delete-interface`, `execute`, etc.).

## Behavior
When the flag is provided:
1. The tool performs all AST parsing, byte-offset calculations, and byte-range replacements in memory.
2. It runs `goimports` on the in-memory buffer.
3. Instead of writing the final buffer to disk, it generates a unified `git diff`-style output comparing the original file content with the in-memory buffer.
4. The tool outputs the diff to `stdout` and exits with a success code (or a specific code to indicate changes exist).

## Benefits
- **Safety:** Allows agents to propose changes to human users for review before applying them.
- **Debugging:** Helps troubleshoot why a specific `update-func` might be failing or formatting strangely.
