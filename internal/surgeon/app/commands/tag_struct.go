package commands

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"regexp"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
)

func (h *ExecutePlanHandler) TagStruct(ctx context.Context, req domain.TagRequest) error {
	if req.Preview {
		child := req
		child.Preview = false
		previewH, _ := h.previewHandler()
		return previewH.TagStruct(ctx, child)
	}
	src, err := h.fs.ReadFile(ctx, req.FilePath)
	if err != nil {
		return &domain.Error{Code: "READ_ERROR", Message: "failed to read file", Err: err}
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, req.FilePath, src, parser.ParseComments)
	if err != nil {
		return &domain.Error{Code: "PARSE_ERROR", Message: "failed to parse file", Err: err}
	}

	var targetStruct *ast.StructType
	for _, decl := range f.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.TYPE {
			for _, spec := range gen.Specs {
				if typeSpec, ok := spec.(*ast.TypeSpec); ok && typeSpec.Name.Name == req.StructName {
					if st, ok := typeSpec.Type.(*ast.StructType); ok {
						targetStruct = st
						break
					}
				}
			}
		}
	}

	if targetStruct == nil {
		return &domain.Error{Code: "NOT_FOUND", Message: fmt.Sprintf("struct '%s' not found", req.StructName)}
	}

	var replacements []tagReplacement

	for _, field := range targetStruct.Fields.List {
		if len(field.Names) == 0 {
			continue // embedded field, skip for now
		}

		var existingTagStr string
		if field.Tag != nil {
			existingTagStr = field.Tag.Value
			// strip backticks
			if len(existingTagStr) >= 2 && existingTagStr[0] == '`' && existingTagStr[len(existingTagStr)-1] == '`' {
				existingTagStr = existingTagStr[1 : len(existingTagStr)-1]
			}
		}

		// A multi-name declaration (x, y int) shares one tag in Go, so any
		// per-name tag change requires splitting the group.
		if len(field.Names) > 1 {
			if rep, ok := groupedTagReplacement(fset, src, field, existingTagStr, req); ok {
				replacements = append(replacements, rep)
			}
			continue
		}

		name := field.Names[0].Name
		if req.FieldName != "" && name != req.FieldName {
			continue
		}
		if req.AutoFormat != "" && !ast.IsExported(name) {
			continue // only auto-tag exported fields
		}

		newTagStr := existingTagStr
		if req.SetTag != "" {
			if req.FieldName != "" {
				// Exact replacement for specific field
				newTagStr = req.SetTag
			} else {
				// Append to all (less likely used this way, but handle it)
				newTagStr = mergeTags(existingTagStr, req.SetTag)
			}
		} else if req.AutoFormat != "" {
			newTagStr = mergeTags(existingTagStr, autoTagValue(name, req.AutoFormat))
		}

		// Unchanged
		if newTagStr == existingTagStr {
			continue
		}

		replacements = append(replacements, tagReplacementFor(fset, field, newTagStr))
	}

	if len(replacements) == 0 {
		return nil // nothing to do
	}

	// Sort replacements backwards to avoid offset shifting
	for i := 0; i < len(replacements)-1; i++ {
		for j := i + 1; j < len(replacements); j++ {
			if replacements[i].start < replacements[j].start {
				replacements[i], replacements[j] = replacements[j], replacements[i]
			}
		}
	}

	updatedSrc := make([]byte, len(src))
	copy(updatedSrc, src)

	for _, rep := range replacements {
		updatedSrc = append(updatedSrc[:rep.start], append([]byte(rep.newText), updatedSrc[rep.end:]...)...)
	}

	if _, err := h.fs.WriteFile(ctx, req.FilePath, updatedSrc); err != nil {
		return &domain.Error{Code: "WRITE_ERROR", Message: "failed to write file", Err: err}
	}

	return nil
}

// tagReplacement is one byte-range substitution in the source file.
type tagReplacement struct {
	start, end int
	newText    string
}

// tagReplacementFor replaces (or inserts) the tag literal of a single field
// declaration.
func tagReplacementFor(fset *token.FileSet, field *ast.Field, newTag string) tagReplacement {
	final := "`" + newTag + "`"
	if field.Tag != nil {
		return tagReplacement{
			start:   fset.Position(field.Tag.Pos()).Offset,
			end:     fset.Position(field.Tag.End()).Offset,
			newText: final,
		}
	}
	// Insert after type
	end := fset.Position(field.Type.End()).Offset
	return tagReplacement{start: end, end: end, newText: " " + final}
}

// autoTagValue builds the auto-generated tag for one field name: camelCase
// keys for json, snake_case for every other format.
func autoTagValue(name, format string) string {
	style := "snake"
	if format == "json" {
		style = "camel"
	}
	return fmt.Sprintf(`%s:"%s"`, format, formatFieldName(name, style))
}

// groupedTagReplacement computes the edit for a multi-name declaration.
// When the requested change gives every member the same tag, the shared tag
// is edited in place; otherwise the declaration is split into one line per
// name so each member carries its own tag (Go attaches a tag to the whole
// declaration, so per-name tags are impossible without splitting).
func groupedTagReplacement(fset *token.FileSet, src []byte, field *ast.Field, existing string, req domain.TagRequest) (tagReplacement, bool) {
	names := field.Names
	newTags := make([]string, len(names))
	for i := range newTags {
		newTags[i] = existing
	}

	switch {
	case req.FieldName != "":
		idx := -1
		for i, n := range names {
			if n.Name == req.FieldName {
				idx = i
				break
			}
		}
		if idx == -1 {
			return tagReplacement{}, false
		}
		if req.SetTag != "" {
			newTags[idx] = req.SetTag
		} else if req.AutoFormat != "" {
			if !ast.IsExported(req.FieldName) {
				return tagReplacement{}, false
			}
			newTags[idx] = mergeTags(existing, autoTagValue(req.FieldName, req.AutoFormat))
		}
	case req.SetTag != "":
		// Append-to-all: every member gets the same tag, group stays intact.
		merged := mergeTags(existing, req.SetTag)
		for i := range newTags {
			newTags[i] = merged
		}
	case req.AutoFormat != "":
		for i, n := range names {
			if ast.IsExported(n.Name) {
				newTags[i] = mergeTags(existing, autoTagValue(n.Name, req.AutoFormat))
			}
		}
	}

	changed, uniform := false, true
	for _, t := range newTags {
		if t != existing {
			changed = true
		}
		if t != newTags[0] {
			uniform = false
		}
	}
	if !changed {
		return tagReplacement{}, false
	}
	if uniform {
		return tagReplacementFor(fset, field, newTags[0]), true
	}

	// Split the declaration: one "Name Type `tag`" line per member.
	typeText := extractSourceRange(src, fset, field.Type.Pos(), field.Type.End())
	indent := lineIndentAt(src, fset.Position(field.Pos()).Offset)
	var b strings.Builder
	for i, n := range names {
		if i > 0 {
			b.WriteString("\n")
			b.WriteString(indent)
		}
		b.WriteString(n.Name)
		b.WriteString(" ")
		b.WriteString(typeText)
		if newTags[i] != "" {
			b.WriteString(" `")
			b.WriteString(newTags[i])
			b.WriteString("`")
		}
	}
	end := fset.Position(field.Type.End()).Offset
	if field.Tag != nil {
		end = fset.Position(field.Tag.End()).Offset
	}
	return tagReplacement{
		start:   fset.Position(field.Pos()).Offset,
		end:     end,
		newText: b.String(),
	}, true
}

// lineIndentAt returns the leading whitespace of the line containing off,
// falling back to a tab when non-whitespace precedes off on that line
// (e.g. a single-line struct).
func lineIndentAt(src []byte, off int) string {
	lineStart := off
	for lineStart > 0 && src[lineStart-1] != '\n' {
		lineStart--
	}
	prefix := string(src[lineStart:off])
	if strings.TrimLeft(prefix, " \t") != "" {
		return "\t"
	}
	return prefix
}

func mergeTags(existing, addition string) string {
	if existing == "" {
		return addition
	}

	// simple merge: just append if not present.
	// Extract the key from addition (e.g. `json:"foo"` -> `json`)
	parts := strings.SplitN(addition, ":", 2)
	if len(parts) > 0 {
		key := parts[0]
		// Check if key exists in struct tag
		if reflect.StructTag(existing).Get(key) != "" {
			return existing // already exists, don't overwrite
		}
	}

	return existing + " " + addition
}

func formatFieldName(name, format string) string {
	if format == "camel" {
		if len(name) == 0 {
			return ""
		}
		if name == "ID" {
			return "id"
		}
		return strings.ToLower(name[:1]) + name[1:]
	}
	// snake case
	var matchFirstCap = regexp.MustCompile("(.)([A-Z][a-z]+)")
	var matchAllCap = regexp.MustCompile("([a-z0-9])([A-Z])")

	snake := matchFirstCap.ReplaceAllString(name, "${1}_${2}")
	snake = matchAllCap.ReplaceAllString(snake, "${1}_${2}")
	return strings.ToLower(snake)
}
