package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// referencesInput is shared between the find_definition and
// find_references MCP tools. They both take the same disambiguators;
// only the output differs.
type referencesInput struct {
	Name              string `json:"name" jsonschema:"symbol name (function, method, type, var, const, or struct field)"`
	Receiver          string `json:"receiver,omitempty" jsonschema:"receiver type name for methods (bare name, no pointer star)"`
	Package           string `json:"package,omitempty" jsonschema:"package import path or name to disambiguate same-named symbols across packages"`
	File              string `json:"file,omitempty" jsonschema:"file path to pin an exact declaration when the name is ambiguous"`
	Line              int    `json:"line,omitempty" jsonschema:"1-based declaration line; pair with file for exact pinning"`
	Dir               string `json:"dir,omitempty" jsonschema:"directory to load packages from (defaults to '.')"`
	Tests             bool   `json:"tests,omitempty" jsonschema:"include _test.go files when resolving and scanning for references"`
	IncludeDefinition bool   `json:"include_definition,omitempty" jsonschema:"on find_references only: also return the definition site; defaults to false"`
}

// renameInput drives the rename_symbol MCP tool.
type renameInput struct {
	Name     string `json:"name" jsonschema:"current symbol name"`
	NewName  string `json:"new_name" jsonschema:"replacement identifier; must be a valid Go identifier, different from name, same export status"`
	Receiver string `json:"receiver,omitempty" jsonschema:"receiver type name for methods (bare name, no pointer star)"`
	Package  string `json:"package,omitempty" jsonschema:"package import path or name for disambiguation"`
	File     string `json:"file,omitempty" jsonschema:"file path to pin an exact declaration"`
	Line     int    `json:"line,omitempty" jsonschema:"1-based declaration line; pair with file for exact pinning"`
	Dir      string `json:"dir,omitempty" jsonschema:"directory to load packages from (defaults to '.')"`
	Tests    bool   `json:"tests,omitempty" jsonschema:"include _test.go files; rewrites them too"`
	Preview  bool   `json:"preview,omitempty" jsonschema:"if true, return the list of sites that would change without writing any file"`
}

// referencesOutput is the structured result for find_references and
// find_definition. Using a common shape keeps the agent-side
// extraction simple.
type referencesOutput struct {
	Name       string           `json:"name"`
	Kind       string           `json:"kind"`
	Package    string           `json:"package,omitempty"`
	Receiver   string           `json:"receiver,omitempty"`
	Definition *locationOutput  `json:"definition,omitempty"`
	References []locationOutput `json:"references,omitempty"`
	Total      int              `json:"total"`
}

// locationOutput is the JSON shape of domain.Location.
type locationOutput struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	LineText string `json:"line_text,omitempty"`
}

// renameOutput is the structured result for rename_symbol.
type renameOutput struct {
	OldName       string           `json:"old_name"`
	NewName       string           `json:"new_name"`
	Kind          string           `json:"kind"`
	FilesModified []string         `json:"files_modified,omitempty"`
	Sites         []locationOutput `json:"sites,omitempty"`
	Total         int              `json:"total"`
	Preview       bool             `json:"preview,omitempty"`
}

// registerReferencesTools wires find_definition and find_references.
func registerReferencesTools(s *mcp.Server, queries service.SurgeonQueries) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "find_definition",
		Description: "Locate the declaration of a Go symbol (function, method, type, var, const, or struct field). Resolution is type-aware via go/packages — works across packages, including when the same name exists in multiple packages. Returns file:line:column of the defining identifier. Pair name with receiver/package/file+line when ambiguous.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in referencesInput) (*mcp.CallToolResult, any, error) {
		if in.Name == "" {
			return errorResult("find_definition: name is required"), nil, nil
		}
		q := domain.ReferencesQuery{
			Symbol: domain.SymbolRef{
				Name:     in.Name,
				Receiver: in.Receiver,
				Package:  in.Package,
				File:     in.File,
				Line:     in.Line,
			},
			Dir:   in.Dir,
			Tests: in.Tests,
		}
		result, err := queries.FindDefinition(ctx, q)
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("find_definition: %v", err), err), nil, nil
		}
		out := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: formatDefinition(result)}},
		}
		out.StructuredContent = definitionStructured(result)
		return out, nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "find_references",
		Description: "Find every reference to a Go symbol across the module, using go/packages type information. Returns file:line:column for each use, deduplicated and sorted. Set include_definition=true to also get the declaration site in one call. Use this before a rename to preview impact.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in referencesInput) (*mcp.CallToolResult, any, error) {
		if in.Name == "" {
			return errorResult("find_references: name is required"), nil, nil
		}
		q := domain.ReferencesQuery{
			Symbol: domain.SymbolRef{
				Name:     in.Name,
				Receiver: in.Receiver,
				Package:  in.Package,
				File:     in.File,
				Line:     in.Line,
			},
			Dir:               in.Dir,
			Tests:             in.Tests,
			IncludeDefinition: in.IncludeDefinition,
		}
		result, err := queries.FindReferences(ctx, q)
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("find_references: %v", err), err), nil, nil
		}
		out := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: formatReferences(result, in.IncludeDefinition)}},
		}
		out.StructuredContent = referencesStructured(result, in.IncludeDefinition)
		return out, nil, nil
	})
}

// registerRenameTool wires rename_symbol.
func registerRenameTool(s *mcp.Server, commands service.SurgeonCommands) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "rename_symbol",
		Description: "Rename a Go symbol and every reference to it across the module. Type-aware: walks go/packages type information so only identifiers that resolve to the same declaration are rewritten (no false positives on same-named but unrelated identifiers). Rejects renames that would change export status or collide with an existing name in the same scope. Set preview=true to list every site without writing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in renameInput) (*mcp.CallToolResult, any, error) {
		if in.Name == "" {
			return errorResult("rename_symbol: name is required"), nil, nil
		}
		if in.NewName == "" {
			return errorResult("rename_symbol: new_name is required"), nil, nil
		}
		r := domain.RenameRequest{
			Symbol: domain.SymbolRef{
				Name:     in.Name,
				Receiver: in.Receiver,
				Package:  in.Package,
				File:     in.File,
				Line:     in.Line,
			},
			NewName: in.NewName,
			Dir:     in.Dir,
			Tests:   in.Tests,
			DryRun:  in.Preview,
		}
		result, err := commands.Rename(ctx, r)
		if err != nil {
			return errorResultWithCode(fmt.Sprintf("rename_symbol: %v", err), err), nil, nil
		}
		out := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: formatRename(result)}},
		}
		out.StructuredContent = renameStructured(result)
		return out, nil, nil
	})
}

// formatDefinition renders the definition result as a compact one-liner
// with a preview of the source line.
func formatDefinition(r domain.ReferencesResult) string {
	if r.Definition.File == "" {
		return fmt.Sprintf("No definition found for %s.", describeSymbol(r))
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s (%s) defined at %s:%d:%d\n", describeSymbol(r), r.Kind, r.Definition.File, r.Definition.Line, r.Definition.Column)
	if r.Definition.LineText != "" {
		fmt.Fprintf(&sb, "  %s\n", strings.TrimSpace(r.Definition.LineText))
	}
	return sb.String()
}

// formatReferences renders the reference list grouped by file with a
// one-line preview per match.
func formatReferences(r domain.ReferencesResult, includeDef bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s (%s): %d reference(s)", describeSymbol(r), r.Kind, len(r.References))
	if includeDef {
		fmt.Fprintf(&sb, " + definition")
	}
	sb.WriteByte('\n')
	if includeDef && r.Definition.File != "" {
		fmt.Fprintf(&sb, "  def  %s:%d:%d", r.Definition.File, r.Definition.Line, r.Definition.Column)
		if r.Definition.LineText != "" {
			fmt.Fprintf(&sb, "  %s", strings.TrimSpace(r.Definition.LineText))
		}
		sb.WriteByte('\n')
	}
	for _, loc := range r.References {
		fmt.Fprintf(&sb, "  ref  %s:%d:%d", loc.File, loc.Line, loc.Column)
		if loc.LineText != "" {
			fmt.Fprintf(&sb, "  %s", strings.TrimSpace(loc.LineText))
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// formatRename renders the rename outcome: summary line + per-file
// count of rewritten sites.
func formatRename(r domain.RenameResult) string {
	verb := "Renamed"
	if r.DryRun {
		verb = "Would rename"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s %s %q → %q: %d site(s) across %d file(s)\n", verb, r.Kind, r.OldName, r.NewName, len(r.Locations), len(r.FilesModified))
	byFile := map[string]int{}
	var order []string
	for _, l := range r.Locations {
		if _, ok := byFile[l.File]; !ok {
			order = append(order, l.File)
		}
		byFile[l.File]++
	}
	for _, f := range order {
		fmt.Fprintf(&sb, "  %s: %d\n", f, byFile[f])
	}
	return sb.String()
}

// describeSymbol renders a human-readable handle for a symbol result
// (covers methods, package-qualified names, bare names).
func describeSymbol(r domain.ReferencesResult) string {
	if r.Symbol.Receiver != "" {
		return fmt.Sprintf("(%s).%s", r.Symbol.Receiver, r.Symbol.Name)
	}
	if r.Symbol.Package != "" {
		return fmt.Sprintf("%s.%s", r.Symbol.Package, r.Symbol.Name)
	}
	return r.Symbol.Name
}

func definitionStructured(r domain.ReferencesResult) referencesOutput {
	out := referencesOutput{
		Name:     r.Symbol.Name,
		Kind:     r.Kind,
		Package:  r.Symbol.Package,
		Receiver: r.Symbol.Receiver,
	}
	if r.Definition.File != "" {
		loc := locationFromDomain(r.Definition)
		out.Definition = &loc
	}
	return out
}

func referencesStructured(r domain.ReferencesResult, includeDef bool) referencesOutput {
	out := referencesOutput{
		Name:     r.Symbol.Name,
		Kind:     r.Kind,
		Package:  r.Symbol.Package,
		Receiver: r.Symbol.Receiver,
		Total:    len(r.References),
	}
	if includeDef && r.Definition.File != "" {
		loc := locationFromDomain(r.Definition)
		out.Definition = &loc
	}
	for _, l := range r.References {
		out.References = append(out.References, locationFromDomain(l))
	}
	return out
}

func renameStructured(r domain.RenameResult) renameOutput {
	out := renameOutput{
		OldName:       r.OldName,
		NewName:       r.NewName,
		Kind:          r.Kind,
		FilesModified: r.FilesModified,
		Total:         len(r.Locations),
		Preview:       r.DryRun,
	}
	for _, l := range r.Locations {
		out.Sites = append(out.Sites, locationFromDomain(l))
	}
	return out
}

func locationFromDomain(l domain.Location) locationOutput {
	return locationOutput{
		File:     l.File,
		Line:     l.Line,
		Column:   l.Column,
		LineText: l.LineText,
	}
}
