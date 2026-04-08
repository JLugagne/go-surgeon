# Error output has redundant "ERROR: ERROR (cmd):" prefix

## Reproduction

Any failing subcommand:

```bash
echo "type FileSystem struct{}" | go-surgeon add-struct --file internal/surgeon/outbound/filesystem/filesystem.go
```

## Current output

```
ERROR: ERROR (add-struct): [NODE_ALREADY_EXISTS] struct "FileSystem" already declared in ...
Hint: use 'update-struct ...' to replace it.
```

## Problem

The output starts with `ERROR: ERROR (add-struct):` — the word "ERROR" appears twice.

**Source of the double prefix:**
1. `RunE` returns `fmt.Errorf("ERROR (%s): %w", name, err)` — adds `ERROR (cmd):`
2. `main.go` prints `fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)` — adds another `ERROR:`

## Expected behavior

Either:
- `main.go` prints without the `ERROR:` prefix (since RunE messages already include it), or  
- `RunE` returns the raw error and `main.go` prefixes it.

Consistent output would be:
```
ERROR (add-struct): [NODE_ALREADY_EXISTS] struct "FileSystem" already declared in ...
Hint: use 'update-struct ...' to replace it.
```
