# scaffold: template-driven project scaffolding with commands, variables, hints, and command chaining

## Motivation

The current scaffolding system is a flat list of files in a single manifest. It can generate files, but it can't **guide a workflow**. An LLM using go-surgeon today has no structured way to know:

- What architectural patterns are available for this project
- What steps to follow and in what order
- What to do **after** the files are generated (define interfaces, write implementations, wire dependencies)

This proposal turns `scaffold` into a real project orchestration system: one template per architecture style, multiple commands per template, command chaining via `post_commands`, and **hints** that print actionable next-step instructions after each command.

## Design

### Template structure

Templates live in `.surgeon-templates/<template-name>/`:

```
.surgeon-templates/
└── hexagonal/
    ├── manifest.yaml
    ├── cmd/
    │   └── appname/
    │       └── main.go.tmpl
    ├── internal/
    │   └── appname/
    │       ├── app/
    │       │   └── app.go.tmpl
    │       ├── domain/
    │       │   ├── errors.go.tmpl
    │       │   └── repositories/
    │       │       └── reponame/
    │       │           └── repo.go.tmpl
    │       ├── inbound/
    │       │   └── http/
    │       │       └── queries/
    │       │           └── router.go.tmpl
    │       └── outbound/
    └── ...
```

The `.tmpl` files mirror the target project layout with placeholder directory names (`appname`, `reponame`) that get substituted via variables.

### Manifest format

```yaml
name: hexagonal
description: |
  Hexagonal architecture template for Go services.
  Provides a full project scaffold with domain-driven design,
  CQRS command/query separation, and ports & adapters pattern.

commands:
  - command: bootstrap
    description: Bootstraps a complete hexagonal project structure
    variables:
      - key: AppName
        description: folder name for the main app
    files:
      - source: .gitignore.tmpl
        destination: .gitignore
      - source: Makefile.tmpl
        destination: Makefile
    post_commands:
      - add_main
      - add_app
    hint: |
      Project bootstrapped. The following commands were executed in sequence.

  - command: add_main
    description: Creates the main.go entrypoint for an application
    variables:
      - key: AppName
        description: folder name for the main app
    files:
      - source: cmd/appname/main.go.tmpl
        destination: cmd/{{ .AppName }}/main.go
    hint: |
      Main created at cmd/{{ .AppName }}/main.go

  - command: add_app
    description: Creates the application layer and domain base files
    variables:
      - key: AppName
        description: folder name for the main app
    files:
      - source: internal/appname/app/app.go.tmpl
        destination: internal/{{ .AppName }}/app/app.go
      - source: internal/appname/domain/errors.go.tmpl
        destination: internal/{{ .AppName }}/domain/errors.go
    post_commands:
      - add_http_inbound
    hint: |
      App structure initialized at internal/{{ .AppName }}/
      You MUST now add repositories using add_repository

  - command: add_repository
    description: Declares a new repository with interface, mock, and contract testing stubs
    variables:
      - key: AppName
        description: folder name for the main app
      - key: RepoName
        description: name for the repository (lowercase)
    files:
      - source: internal/appname/domain/repositories/reponame/repo.go.tmpl
        destination: internal/{{ .AppName }}/domain/repositories/{{ .RepoName }}/repo.go
    hint: |
      New repo created in internal/{{ .AppName }}/domain/repositories/{{ .RepoName }}/, you MUST:
        - define the repository interface in repo.go
        - generate the mock in ./{{ .RepoName }}test/mock.go using go-surgeon add-interface
        - define the contract tests in ./{{ .RepoName }}test/contracts.go
        - implement the adapter in internal/{{ .AppName }}/outbound/<adapter>/{{ .RepoName }}.go

  - command: add_http_inbound
    description: Creates an HTTP inbound adapter with query/command routers
    variables:
      - key: AppName
        description: folder name for the main app
    files:
      - source: internal/appname/inbound/http/queries/router.go.tmpl
        destination: internal/{{ .AppName }}/inbound/http/queries/router.go
      - source: internal/appname/inbound/http/commands/router.go.tmpl
        destination: internal/{{ .AppName }}/inbound/http/commands/router.go
    hint: |
      HTTP inbound created. You MUST:
        - add query handlers in internal/{{ .AppName }}/inbound/http/queries/
        - add command handlers in internal/{{ .AppName }}/inbound/http/commands/
        - wire the routers in internal/{{ .AppName }}/app/app.go
```

### Command chaining with `post_commands`

A command can declare `post_commands`: a list of command names from the same template that are executed **automatically in sequence** after the parent command completes.

When `go-surgeon scaffold execute hexagonal bootstrap --var AppName="catalog"` runs:

1. Execute `bootstrap` → generates `.gitignore`, `Makefile`
2. Execute `add_main` (post_command of bootstrap) → generates `cmd/catalog/main.go`
3. Execute `add_app` (post_command of bootstrap) → generates `app.go`, `errors.go`
4. Execute `add_http_inbound` (post_command of add_app) → generates routers

Post-commands inherit all variables from the parent invocation. The chain is walked recursively (depth-first): if `add_app` has its own `post_commands`, those execute before moving to the next sibling in the parent's list.

After all commands in the chain complete, **all hints from the entire chain** are printed in execution order:

```
SUCCESS: Executed hexagonal/bootstrap (4 commands)

Created files:
  .gitignore
  Makefile
  cmd/catalog/main.go
  internal/catalog/app/app.go
  internal/catalog/domain/errors.go
  internal/catalog/inbound/http/queries/router.go
  internal/catalog/inbound/http/commands/router.go

--- bootstrap ---
Project bootstrapped. The following commands were executed in sequence.

--- add_main ---
Main created at cmd/catalog/main.go

--- add_app ---
App structure initialized at internal/catalog/
You MUST now add repositories using add_repository

--- add_http_inbound ---
HTTP inbound created. You MUST:
  - add query handlers in internal/catalog/inbound/http/queries/
  - add command handlers in internal/catalog/inbound/http/commands/
  - wire the routers in internal/catalog/app/app.go
```

The LLM reads this output and knows exactly what to do next.

### Variable aggregation with dedup

When `doc` displays a command that has `post_commands`, it walks the entire tree and lists all required variables **deduplicated by key** (first description wins, depth-first):

```bash
go-surgeon scaffold doc hexagonal bootstrap
```

```
Command: bootstrap
  Bootstraps a complete hexagonal project structure

  Executes: bootstrap → add_main → add_app → add_http_inbound

  Variables (all commands, deduplicated):
    --var AppName    folder name for the main app
```

`AppName` appears in all four commands but is listed once. For a command that introduces new variables deeper in the tree:

```bash
go-surgeon scaffold doc hexagonal add_repository
```

```
Command: add_repository
  Declares a new repository with interface, mock, and contract testing stubs

  Variables:
    --var AppName     folder name for the main app
    --var RepoName    name for the repository (lowercase)
```

The same dedup logic applies to `execute` validation: all variables from the full `post_commands` tree are collected, deduplicated, and checked before any file is generated.

### CLI

```bash
# List all templates
go-surgeon scaffold list-templates

# Show documentation for a template (all commands)
go-surgeon scaffold doc <template>

# Show documentation for a specific command (with aggregated post_commands tree)
go-surgeon scaffold doc <template> <command>

# Execute a command (with automatic post_commands chaining)
go-surgeon scaffold execute <template> <command> --var AppName="catalog" --var RepoName="book"
```

**Validation error when variables are missing:**

```
ERROR: missing required variables for hexagonal/bootstrap:

  --var AppName    folder name for the main app

Usage: go-surgeon scaffold execute hexagonal bootstrap --var AppName="catalog"
```

### Execution flow

When `go-surgeon scaffold execute hexagonal bootstrap --var AppName="catalog"` runs:

1. **Parse**: Read `.surgeon-templates/hexagonal/manifest.yaml`
2. **DAG validation**: Build the `post_commands` graph for the entire manifest. Verify it is a DAG — if a cycle is detected, error at this step with the cycle path (e.g. `ERROR: cycle detected: bootstrap → add_app → add_http_inbound → add_app`). This validation runs once at manifest load time, not per-execution.
3. **Resolve command tree**: Find command `bootstrap`, walk its `post_commands` tree recursively (depth-first)
4. **Collect variables**: Gather all required variables from the full tree, deduplicated by key (first description wins in depth-first order)
5. **Validate variables**: Check all required variables are provided → error with the missing list if not
6. **Execute with dedup**: Walk the tree depth-first. Maintain a **visited set** of command names. For each command encountered:
   - If already in visited → **skip silently** (do not execute, do not collect hint)
   - If not visited → mark as visited, execute files, collect rendered hint
7. **Format**: Run `goimports` on all generated `.go` files (batched, once at the end)
8. **Report**: Print summary — all created files, then all hints in execution order with command headers (only for commands that actually executed)

#### Dedup in practice

A command can appear in multiple `post_commands` lists — this is expected (diamond dependencies). The guarantee is: **each command executes at most once per `scaffold execute` invocation.** First encounter wins, subsequent references are skipped.

Example: if both `add_app` and `add_cli_inbound` list `add_config` in their `post_commands`:

```yaml
commands:
  - command: bootstrap
    post_commands: [add_app, add_cli_inbound]

  - command: add_app
    post_commands: [add_config]

  - command: add_cli_inbound
    post_commands: [add_config]

  - command: add_config
    # ...
```

Depth-first walk: `bootstrap` → `add_app` → `add_config` → `add_cli_inbound` → ~~`add_config`~~ (skipped, already visited).

Output reflects only what actually ran:

```
SUCCESS: Executed hexagonal/bootstrap (4 commands, 1 skipped)

Created files:
  ...

--- bootstrap ---
[hint]

--- add_app ---
[hint]

--- add_config ---
[hint]

--- add_cli_inbound ---
[hint]
```

### DAG validation

The `post_commands` graph is validated as a strict DAG at manifest parse time. This is a separate concern from execution dedup — it catches structural errors in the manifest before anything runs.

**Algorithm**: For each command in the manifest, walk its `post_commands` references with a recursion stack. If a command is encountered that is already on the current recursion stack, it's a cycle.

```
ERROR: invalid manifest "hexagonal": cycle detected in post_commands: add_app → add_http_inbound → add_app
```

This validation runs:
- On `scaffold execute` — before any files are generated
- On `scaffold doc` — before printing documentation
- NOT on `scaffold list-templates` — listing is lightweight, only reads name + description

**What is allowed**: A command referenced by multiple parents (diamond). What is forbidden: a command that directly or transitively references itself (cycle).

### Template FuncMap

Available in file templates, destination paths, and hints:

- `lower`, `upper`, `title` (existing)
- `camel` — `order_item` → `OrderItem`
- `snake` — `OrderItem` → `order_item`
- `kebab` — `OrderItem` → `order-item`
- `plural` — `order` → `orders`

## Files to change

1. `internal/surgeon/domain/action.go` — new types: `Template` (name, description, commands), `TemplateCommand` (command, description, variables, files, post_commands, hint), `TemplateVariable` (key, description), `TemplateFile` (source, destination)
2. `internal/surgeon/app/commands/scaffolder.go` — rewrite to: scan `.surgeon-templates/*/manifest.yaml`, DAG validation on parse, resolve source paths relative to template dir, walk `post_commands` tree with visited set for execution dedup, execute hint templates, batch `goimports` at end
3. `internal/surgeon/inbound/cli/commands/scaffold.go` — replace single command with `list-templates`, `doc`, `execute` subcommands. `doc` walks `post_commands` tree for aggregated deduplicated variable display. `execute` parses `--var Key="value"` arguments.
4. `internal/surgeon/domain/service/scaffolder.go` — update interface: `ListTemplates`, `DocTemplate`, `DocCommand`, `Execute`
5. `internal/surgeon/init.go` — wire the new subcommands

## Edge cases

- **Cycles in `post_commands`**: detected at manifest parse time via recursion stack walk. Error with the full cycle path. Blocks execution entirely.
- **Diamond dependencies** (same command referenced by multiple parents): allowed. The command executes once — first encounter in depth-first walk. Subsequent encounters are skipped silently.
- **Missing `post_commands` reference**: error at parse time if a command references a non-existent command name
- **Variable key conflicts**: two commands in the chain declare the same key with different descriptions — keep the first description encountered (depth-first walk order), log a warning
- **File already exists**: current behavior (error) is preserved. The hint system means the LLM knows not to run the same command twice
- **Empty `post_commands`**: treated as no chaining, only the command itself executes
- **`post_commands` on a leaf command**: valid, the command runs its files + hint, no further chaining
- **Skipped command count**: the success message reports both executed and skipped counts for transparency

## Backward compatibility

The old `.templates/manifest.yaml` path and `go-surgeon scaffold <command>` syntax are no longer supported. This is a breaking change for the `scaffold` command. Migration: move templates into `.surgeon-templates/<n>/` and update manifest keys (`command` instead of `name`, `source`/`destination` instead of `path`/`template`, add `variables` with explicit keys).
