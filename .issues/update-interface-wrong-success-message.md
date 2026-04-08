# update-interface: incorrect success message when identifier not found (fallback to add)

## Reproduction

```bash
echo "type FakeInterface interface{}" | go-surgeon update-interface --file internal/surgeon/app/queries/symbol.go --id NonExistentInterface
```

## Current output

```
SUCCESS: Updated NonExistentInterface in symbol.go (WARNING: update_struct: identifier "NonExistentInterface" not found in symbol.go, treated as add_struct)
```

## Problem

The success message says "Updated NonExistentInterface" but the interface was actually added (appended) as `FakeInterface` — the name from the content, not the `--id` value. The warning is accurate, but it is buried in parentheses after the success message, making it easy to miss.

This is the same root cause as the `update-func` issue: the identifier in `--id` and the type name in the content can diverge silently.

## Expected behavior

When the fallback to add_struct is triggered, the SUCCESS line should reflect what actually happened:
```
SUCCESS: Added FakeInterface to symbol.go (NOTE: '--id NonExistentInterface' not found, content was appended as a new declaration)
```

The warning should also appear on its own line (as `update-func` does), not inline with the success message.
