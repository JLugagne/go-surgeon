# update-func fallback warning: hint shows the --id value, not the actual function name written

## Reproduction

```bash
echo "func NewFakeFunc() {}" | go-surgeon update-func --file internal/surgeon/domain/errors.go --id NonExistentFunc
```

## Current output

```
WARNING (update-func): update_func: identifier "NonExistentFunc" not found in errors.go, treated as add_func
Hint: verify '--id NonExistentFunc' is the exact identifier. Use 'go-surgeon symbol NonExistentFunc' to confirm.
SUCCESS (update-func): Updated NonExistentFunc in internal/surgeon/domain/errors.go
```

## Problem

The hint says `go-surgeon symbol NonExistentFunc` but the function that was actually written to the file is `NewFakeFunc` (from the content). After the command runs, `go-surgeon symbol NonExistentFunc` will return "no matches found". The user is guided to look for the wrong identifier.

The SUCCESS line also says "Updated NonExistentFunc" which is incorrect — it added `NewFakeFunc`.

## Root cause

The success message and hint both use the `id` flag value (`NonExistentFunc`), but the actual content written to the file has its own function name (`NewFakeFunc`). The two can be completely different.

## Expected behavior

The hint should say something like:
```
Hint: '--id NonExistentFunc' not found; content was appended as a new function. Run 'go-surgeon graph -s -d <dir>' to verify what was written.
```

The SUCCESS message should not claim the identifier was updated — it was appended.
