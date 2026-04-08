# Feature: Struct Tag Manipulation

## Motivation
Modifying struct tags (e.g., adding `json:"omitempty"` or `validate:"required"`) currently requires an LLM to replace the entire struct definition using `update-struct`. This is risky for very large structs and wastes tokens.

## Design
Add a dedicated command for surgical tag manipulation:
`go-surgeon tag --file <path> --id <StructName> [--field <FieldName>] [--set <tag_string>] [--auto <tag_format>]`

## Behavior
1. **Specific Field Update:**
   `go-surgeon tag --file user.go --id User --field Email --set 'json:"email,omitempty" validate:"required"'`
   - Replaces or adds the exact tag string to the `Email` field.

2. **Automated Bulk Tagging:**
   `go-surgeon tag --file user.go --id User --auto json`
   - Iterates over all exported fields in the struct.
   - If a field lacks a `json` tag, it automatically generates one based on the field name (e.g., `camelCase` or `snake_case` depending on configuration/defaults).
   - **Crucial:** It must *append* to existing tags, not overwrite them. If a field already has a `bson:"..."` tag, the new `json:"..."` tag is safely appended alongside it.

## Benefits
- High precision: modifies only the AST node for the tag.
- Fast bulk operations for DTOs and database models.
