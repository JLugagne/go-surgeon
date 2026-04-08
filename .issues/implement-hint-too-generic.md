# implement: error hint is misleading when --file doesn't exist

## Reproduction

```bash
go-surgeon implement io.Closer -r "*MyStruct" -f /tmp/nonexistent.go
```

## Current output

```
ERROR: failed to implement interface: failed to read file /tmp/nonexistent.go: open /tmp/nonexistent.go: no such file or directory
Hint: use the full import path (e.g., 'github.com/myorg/myapp/domain.Interface'). For project-local interfaces, prefer 'add-interface'.
```

## Problem

The hint fires for all `implement` errors, but "file not found" is unrelated to the import path. The hint points the user in the wrong direction.

## Expected behavior

The hint should match the actual error:
- File not found → `Hint: create the target file first, or check the '--file' path.`
- Interface resolution failure → the current full-import-path hint is correct.

## Fix

In `NewImplementCommand.RunE`, inspect the error type before choosing the hint. Check for `os.IsNotExist` or the file-not-found message to select a file-specific hint vs the import-path hint.
