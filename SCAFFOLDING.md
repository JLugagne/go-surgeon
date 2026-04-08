# Template-Driven Scaffolding

`go-surgeon scaffold` goes beyond generating flat files. It is a workflow engine designed to guide developers (and AI agents) through complex architectural implementations. 

By grouping files into `commands`, declaring required `variables`, and chaining commands together via `post_commands`, a single template can orchestrate the creation of a full project and provide step-by-step `hints` on what to do next.

## Template Structure

Templates live in the `.surgeon-templates/` directory at the root of your project:

```
.surgeon-templates/
└── hexagonal/
    ├── manifest.yaml
    ├── .gitignore.tmpl
    ├── Makefile.tmpl
    └── cmd/
        └── appname/
            └── main.go.tmpl
```

## `manifest.yaml`

The manifest defines the template and its commands.

```yaml
name: hexagonal
description: |
  Hexagonal architecture template for Go services.

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
      Main created at cmd/{{ .AppName }}/main.go.
```

### Components of a Command

#### `variables`
A list of variables required to render the files and hints. Variables are passed via the CLI using `--set Key=Value` (e.g. `--set AppName=catalog`). 

Variables are exposed in templates using standard Go `text/template` syntax (e.g. `{{ .AppName }}`).

#### `files`
A list of files to generate. 
- `source`: Path to the template file relative to the template directory (e.g. `.surgeon-templates/hexagonal/cmd/appname/main.go.tmpl`). If omitted, creates an empty file.
- `destination`: Target path in your project. This path itself is rendered as a template, meaning you can use variables directly in file and folder names (e.g. `cmd/{{ .AppName }}/main.go`).

#### `post_commands`
A list of other command names to execute **automatically in sequence** after the current command finishes. 

This enables workflow chaining: you can run `bootstrap`, which automatically triggers `add_main`, which triggers `add_http_inbound`, etc.

The variables passed to the parent command are automatically forwarded to the `post_commands`. The execution engine deduplicates commands to ensure that a command is never executed twice in the same chain (diamond dependency resolution).

#### `hint`
A message to print to the console after the command completes. Hints are extremely useful for guiding AI agents on what steps to take *after* the scaffolding is done. 

Hints are also processed as Go templates, meaning you can use your variables within them:

```yaml
    hint: |
      App structure initialized at internal/{{ .AppName }}/
      You MUST now add repositories using add_repository
```

## Available Template Functions

You can use the following functions in your file content, destination paths, and hints:

- `lower`: converts to lowercase (e.g. `{{ .AppName | lower }}`)
- `upper`: converts to uppercase (e.g. `{{ .AppName | upper }}`)
- `title`: capitalizes the first letter (e.g. `{{ .AppName | title }}`)

## Best Practices for AI Agents

1. **Granular Commands:** Break down your architecture into granular commands (`add_repository`, `add_usecase`, `add_http_handler`) rather than one massive generator.
2. **Actionable Hints:** Use the `hint` field to print a "Task List" of the next logical commands the agent should run. LLMs process these hints and act on them iteratively.
3. **Safety First:** `go-surgeon` performs a strict "pre-flight check" before executing a chain of commands. If any destination file already exists, it aborts the entire execution to prevent partial overwrites.
