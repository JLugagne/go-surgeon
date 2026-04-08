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

	type replacement struct {
		start, end int
		newText    string
	}
	var replacements []replacement

	for _, field := range targetStruct.Fields.List {
		if len(field.Names) == 0 {
			continue // embedded field, skip for now
		}

		name := field.Names[0].Name
		if req.FieldName != "" && name != req.FieldName {
			continue
		}

		// determine new tag
		isExported := ast.IsExported(name)
		if req.AutoFormat != "" && !isExported {
			continue // only auto-tag exported fields
		}

		var existingTagStr string
		if field.Tag != nil {
			existingTagStr = field.Tag.Value
			// strip backticks
			if len(existingTagStr) >= 2 && existingTagStr[0] == '`' && existingTagStr[len(existingTagStr)-1] == '`' {
				existingTagStr = existingTagStr[1 : len(existingTagStr)-1]
			}
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
			formattedName := formatFieldName(name, "snake") // default to snake, maybe allow config
			if req.AutoFormat == "json" || req.AutoFormat == "bson" {
				// simple snake case for auto
				// Actually standard json format might be camelCase or snake_case. Let's do camelCase for json.
				if req.AutoFormat == "json" {
					formattedName = formatFieldName(name, "camel")
				}
			}
			autoTag := fmt.Sprintf(`%s:"%s"`, req.AutoFormat, formattedName)
			newTagStr = mergeTags(existingTagStr, autoTag)
		}

		// Unchanged
		if newTagStr == existingTagStr {
			continue
		}

		finalTag := "`" + newTagStr + "`"

		if field.Tag != nil {
			start := fset.Position(field.Tag.Pos()).Offset
			end := fset.Position(field.Tag.End()).Offset
			replacements = append(replacements, replacement{start: start, end: end, newText: finalTag})
		} else {
			// Insert after type
			end := fset.Position(field.Type.End()).Offset
			replacements = append(replacements, replacement{start: end, end: end, newText: " " + finalTag})
		}
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

	if err := h.fs.WriteFile(ctx, req.FilePath, updatedSrc); err != nil {
		return &domain.Error{Code: "WRITE_ERROR", Message: "failed to write file", Err: err}
	}
	h.fs.ExecuteGoImports(ctx, []string{req.FilePath})

	return nil
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
	var matchAllCap   = regexp.MustCompile("([a-z0-9])([A-Z])")

	snake := matchFirstCap.ReplaceAllString(name, "${1}_${2}")
	snake = matchAllCap.ReplaceAllString(snake, "${1}_${2}")
	return strings.ToLower(snake)
}
