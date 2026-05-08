package discovery

import (
	"fmt"
	"sort"
	"strings"
)

// Render returns text for the given query, mirroring the old
// describe_tool MCP semantics:
//   - name == "" && category == ""  → grouped list
//   - name != ""                    → single tool detail (or tool.op detail when name contains a '.')
//   - category != ""                → filtered list
//
// name and category are mutually exclusive. An unknown tool/op/category
// returns an error.
func Render(name, category string) (string, error) {
	if name != "" && category != "" {
		return "", fmt.Errorf("name and category are mutually exclusive")
	}
	if name != "" {
		if tool, op, ok := splitToolOp(name); ok {
			return renderOp(tool, op)
		}
		return renderOne(name)
	}
	return renderList(category)
}

func renderOne(name string) (string, error) {
	for _, e := range Catalog {
		if e.Name == name {
			var sb strings.Builder
			fmt.Fprintf(&sb, "%s (%s)\n", e.Name, e.Category)
			fmt.Fprintf(&sb, "  %s\n", e.Summary)
			if e.Example != "" {
				fmt.Fprintf(&sb, "  example: %s\n", e.Example)
			}
			if e.Related != "" {
				fmt.Fprintf(&sb, "  see also: %s\n", e.Related)
			}
			if len(e.Limitations) > 0 {
				sb.WriteString("  limitations:\n")
				for _, lim := range e.Limitations {
					fmt.Fprintf(&sb, "    - %s\n", lim)
				}
			}
			if len(e.Ops) > 0 {
				sb.WriteString("  ops: ")
				sb.WriteString(strings.Join(SortedOpKeys(e.Ops), ", "))
				sb.WriteByte('\n')
				fmt.Fprintf(&sb, "  → run `go-surgeon discovery %s.<op>` for op detail\n", e.Name)
			}
			return sb.String(), nil
		}
	}
	return "", fmt.Errorf("unknown tool %q (run `go-surgeon discovery` to see the catalog)", name)
}

func renderList(category string) (string, error) {
	if category != "" {
		if _, ok := CategoryLabels[category]; !ok {
			return "", fmt.Errorf("unknown category %q (valid: %s)", category, strings.Join(CategoryOrder, ", "))
		}
	}
	grouped := map[string][]ToolEntry{}
	for _, e := range Catalog {
		if strings.Contains(e.Name, ".") {
			continue // skip pseudo-entries like patch.function
		}
		grouped[e.Category] = append(grouped[e.Category], e)
	}
	for k := range grouped {
		rows := grouped[k]
		sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
		grouped[k] = rows
	}
	var sb strings.Builder
	for _, cat := range CategoryOrder {
		if category != "" && cat != category {
			continue
		}
		rows := grouped[cat]
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "%s\n", CategoryLabels[cat])
		for _, r := range rows {
			fmt.Fprintf(&sb, "  %-20s %s\n", r.Name, r.Summary)
		}
		sb.WriteByte('\n')
	}
	if sb.Len() == 0 {
		return "(no tools matched)\n", nil
	}
	sb.WriteString("Run `go-surgeon discovery <tool>` for per-tool detail.\n")
	return sb.String(), nil
}

func renderOp(toolName, op string) (string, error) {
	var entry ToolEntry
	found := false
	for _, e := range Catalog {
		if e.Name == toolName {
			entry = e
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("unknown tool %q (run `go-surgeon discovery` to see the catalog)", toolName)
	}
	if len(entry.Ops) == 0 {
		return "", fmt.Errorf("tool %q has no ops", toolName)
	}
	opEntry, ok := entry.Ops[op]
	if !ok {
		return "", fmt.Errorf("unknown op %q on %s (known ops: %s)", op, toolName, strings.Join(SortedOpKeys(entry.Ops), ", "))
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s.%s (%s)\n", entry.Name, op, entry.Category)
	fmt.Fprintf(&sb, "  %s\n", opEntry.Description)
	if len(opEntry.Required) > 0 {
		fmt.Fprintf(&sb, "  required: %s\n", strings.Join(opEntry.Required, ", "))
	}
	if opEntry.Example != "" {
		fmt.Fprintf(&sb, "  example: %s\n", opEntry.Example)
	}
	return sb.String(), nil
}

func splitToolOp(name string) (string, string, bool) {
	idx := strings.IndexByte(name, '.')
	if idx <= 0 || idx == len(name)-1 {
		return name, "", false
	}
	return name[:idx], name[idx+1:], true
}

// SortedOpKeys returns op keys in canonical-then-alphabetic order.
func SortedOpKeys(ops map[string]ToolOpEntry) []string {
	if canonical := canonicalOpOrder(ops); canonical != nil {
		return canonical
	}
	keys := make([]string, 0, len(ops))
	for k := range ops {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func canonicalOpOrder(ops map[string]ToolOpEntry) []string {
	orders := [][]string{
		{"function", "struct", "interface", "file", "decl"},
		{"replace", "insert_before", "insert_after", "delete", "wrap", "set_signature"},
		{"add_field", "remove_field", "rename_field", "retype_field", "set_tag", "set_doc", "auto_tag"},
		{"add_method", "remove_method", "rename_method", "retype_method", "set_doc", "embed", "remove_embed"},
		{"replace", "insert_before", "insert_after", "delete", "wrap"},
	}
	for _, order := range orders {
		if len(order) != len(ops) {
			continue
		}
		match := true
		for _, k := range order {
			if _, ok := ops[k]; !ok {
				match = false
				break
			}
		}
		if match {
			return order
		}
	}
	return nil
}
