# go-surgeon AI Instructions

To help AI agents (like Claude, GPT-4, Cursor, or Copilot) use `go-surgeon` effectively in your project, copy the following instructions into your project's `.cursorrules`, `AI_INSTRUCTIONS.md`, or system prompt.

---

## Copy below this line

```markdown
# Go Code Editing Rules

You are managing a Go codebase. To read, navigate, and modify Go code, you MUST use the `go-surgeon` CLI. DO NOT use generic tools like `cat`, `sed`, `grep`, or output full file replacements using standard diffs, as they lead to indentation errors and context limit exhaustion.

`go-surgeon` is a deterministic AST-based byte-range editor. It automatically runs `goimports`, meaning you NEVER need to worry about formatting or import statements when writing code.

## 1. Orientation & Navigation
Always start by exploring the codebase using `go-surgeon` rather than reading full files. Use context window management flags to avoid blowing up your token budget on large projects.

- **List all packages:** `go-surgeon graph`
- **High-level overview:** `go-surgeon graph --summary --depth 2`
- **Zoom into one package:** `go-surgeon graph --focus <package_path>` (full detail for the target, path-only for the rest)
- **List all exported symbols in a package:** `go-surgeon graph -s -d <relative_dir>`
- **Exclude directories:** `go-surgeon graph --exclude vendor --exclude "*legacy*"`
- **Fit within token budget:** `go-surgeon graph --summary --deps --token-budget 2000`
- **Read a function, struct, or method:** `go-surgeon symbol <Name> --body` (Use `Receiver.Method` for precise method lookups).

## 2. Editing Code
When modifying code, stream the raw Go declaration (without `package` or `import` blocks) via stdin to the specific `go-surgeon` subcommand.

**Rules for Editing:**
- Always provide the FULL declaration (complete signature and body).
- Do not add `package` or `import` at the top of your snippets.
- Use `update-func` or `update-struct` to replace existing nodes.
- Use `add-func` or `add-struct` to append to a file.

**Example: Updating a Method**
```bash
cat <<'EOF' | go-surgeon update-func --file internal/app/service.go --id "(*Service).DoWork"
func (s *Service) DoWork(ctx context.Context) error {
    // your new logic here
    return nil
}
EOF
```

## 3. Interfaces & Mocks
When creating or modifying interfaces, always use the dedicated interface commands so the mock is generated and kept in sync automatically.

```bash
cat <<'EOF' | go-surgeon update-interface --file domain/repo.go --id Repository --mock-file domain/repotest/mock.go --mock-name MockRepository
type Repository interface {
    Save(ctx context.Context, item Item) error
}
EOF
```

To stub out missing methods for an interface you are implementing:
```bash
go-surgeon implement io.Reader --receiver "*MyReader" --file reader.go
```

```
