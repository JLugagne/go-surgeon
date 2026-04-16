package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: msg},
		},
		IsError: true,
	}
}

// formatGraph renders packages the same way the CLI graph command does.
func formatGraph(packages []domain.GraphPackage, opts domain.GraphOptions) string {
	if !opts.Symbols {
		if len(packages) == 0 {
			return fmt.Sprintf("No Go packages found in '%s'.", opts.Dir)
		}
		var sb strings.Builder
		for _, pkg := range packages {
			line := pkg.Path
			if pkg.Summary != "" {
				line += " — " + pkg.Summary
			}
			if len(pkg.Deps) > 0 {
				line += " → " + strings.Join(pkg.Deps, ", ")
			} else if opts.Deps {
				line += " → (none)"
			}
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
		return sb.String()
	}

	showHeader := opts.Summary || opts.Deps
	var sb strings.Builder
	first := true

	for _, pkg := range packages {
		hasFiles := len(pkg.Files) > 0
		hasHeader := showHeader && (pkg.Summary != "" || len(pkg.Deps) > 0)
		if !hasHeader && !hasFiles {
			if opts.Focus != "" {
				if !first {
					sb.WriteByte('\n')
				}
				sb.WriteString(pkg.Path)
				sb.WriteByte('\n')
				first = false
			}
			continue
		}

		if !first {
			sb.WriteByte('\n')
		}
		first = false

		if showHeader {
			line := pkg.Path
			if pkg.Summary != "" {
				line += " — " + pkg.Summary
			}
			if opts.Deps {
				if len(pkg.Deps) > 0 {
					line += " → " + strings.Join(pkg.Deps, ", ")
				} else {
					line += " → (none)"
				}
			}
			sb.WriteString(line)
			sb.WriteByte('\n')
		}

		for _, file := range pkg.Files {
			sb.WriteString(file.Path)
			sb.WriteByte('\n')
			for _, sym := range file.Symbols {
				for _, line := range strings.Split(sym, "\n") {
					sb.WriteString("  ")
					sb.WriteString(line)
					sb.WriteByte('\n')
				}
			}
		}
	}

	if sb.Len() == 0 {
		return fmt.Sprintf("No Go packages found in '%s'.", opts.Dir)
	}

	return sb.String()
}

// findSymbols mirrors the CLI symbol command's multi-form query resolution.
func findSymbols(ctx context.Context, queries service.SurgeonQueries, queryStr string, tests bool, dir string, module string, context string) []domain.SymbolResult {
	parts := strings.Split(queryStr, ".")
	var allResults []domain.SymbolResult

	if len(parts) == 1 {
		query := domain.SymbolQuery{Name: parts[0], Tests: tests, Module: module, Context: context}
		results, _ := queries.FindSymbols(ctx, query, dir)
		allResults = append(allResults, results...)
	}

	if len(parts) == 2 {
		query1 := domain.SymbolQuery{Receiver: parts[0], Name: parts[1], Tests: tests, Module: module, Context: context}
		results1, _ := queries.FindSymbols(ctx, query1, dir)
		allResults = append(allResults, results1...)

		query2 := domain.SymbolQuery{PackageName: parts[0], Name: parts[1], Tests: tests, Module: module, Context: context}
		results2, _ := queries.FindSymbols(ctx, query2, dir)
		allResults = append(allResults, results2...)
	}

	if len(parts) == 3 {
		query := domain.SymbolQuery{PackageName: parts[0], Receiver: parts[1], Name: parts[2], Tests: tests, Module: module, Context: context}
		results, _ := queries.FindSymbols(ctx, query, dir)
		allResults = append(allResults, results...)
	}

	return allResults
}

// formatSymbolResults renders symbol results the same way the CLI symbol command does.
func formatSymbolResults(results []domain.SymbolResult, showBody bool, queryStr string) string {
	var sb strings.Builder

	if len(results) > 1 {
		fmt.Fprintf(&sb, "Found %d matches for '%s'. Please refine your search:\n\n", len(results), queryStr)
		var funcs, methods, structs []domain.SymbolResult
		for _, r := range results {
			if r.Receiver != "" {
				methods = append(methods, r)
			} else if strings.HasPrefix(r.Signature, "func") {
				funcs = append(funcs, r)
			} else {
				structs = append(structs, r)
			}
		}
		if len(methods) > 0 {
			sb.WriteString("Matches (Methods):\n")
			for _, r := range methods {
				fmt.Fprintf(&sb, "- (%s) %s in %s:%d\n", r.Receiver, r.Name, r.File, r.LineStart)
			}
			sb.WriteByte('\n')
		}
		if len(funcs) > 0 {
			sb.WriteString("Matches (Functions):\n")
			for _, r := range funcs {
				fmt.Fprintf(&sb, "- %s in %s:%d\n", r.Name, r.File, r.LineStart)
			}
			sb.WriteByte('\n')
		}
		if len(structs) > 0 {
			sb.WriteString("Matches (Structs):\n")
			for _, r := range structs {
				fmt.Fprintf(&sb, "- %s in %s:%d\n", r.Name, r.File, r.LineStart)
			}
			sb.WriteByte('\n')
		}
		var hintCmd string
		if len(methods) > 0 {
			first := methods[0]
			hintCmd = fmt.Sprintf("symbol query=%s.%s", first.Receiver, first.Name)
		} else if len(funcs) > 0 {
			first := funcs[0]
			hintCmd = fmt.Sprintf("symbol query=%s dir=%s", first.Name, filepath.Dir(first.File))
		} else {
			first := structs[0]
			hintCmd = fmt.Sprintf("symbol query=%s dir=%s", first.Name, filepath.Dir(first.File))
		}
		fmt.Fprintf(&sb, "Hint: refine with '%s'.\n", hintCmd)
		return sb.String()
	}

	res := results[0]
	fmt.Fprintf(&sb, "Symbol: %s\n", res.Name)
	if res.Receiver != "" {
		fmt.Fprintf(&sb, "Receiver: %s\n", res.Receiver)
	}
	bodyLines := res.LineEnd - res.LineStart + 1
	fmt.Fprintf(&sb, "File: %s:%d-%d (%d lines body)\n", res.File, res.LineStart, res.LineEnd, bodyLines)
	if showBody {
		// Package + imports header: gives the agent enough ambient context
		// to patch without a follow-up Read (covers "do I need to add an
		// import?" and "what package is this in?" in one response).
		if res.Package != "" {
			fmt.Fprintf(&sb, "Package: %s\n", res.Package)
		}
		if len(res.Imports) > 0 {
			sb.WriteString("Imports:\n")
			for _, imp := range res.Imports {
				fmt.Fprintf(&sb, "  %s\n", imp)
			}
		}
		if len(res.FileOutline) > 0 {
			sb.WriteString("File outline:\n")
			for _, e := range res.FileOutline {
				if e.Receiver != "" {
					fmt.Fprintf(&sb, "  L%d-%d (%s) %s.%s: %s\n", e.LineStart, e.LineEnd, e.Kind, e.Receiver, e.Name, e.Signature)
				} else {
					fmt.Fprintf(&sb, "  L%d-%d (%s) %s: %s\n", e.LineStart, e.LineEnd, e.Kind, e.Name, e.Signature)
				}
			}
		}
	}
	if res.Doc != "" {
		fmt.Fprintf(&sb, "Doc:\n%s\n", res.Doc)
	}
	if showBody {
		fmt.Fprintf(&sb, "Code (Empty lines stripped):\n%s\n", res.Code)
	} else {
		fmt.Fprintf(&sb, "Signature:\n%s\n", res.Signature)
	}
	return sb.String()
}

func validateGoFile(file string) *mcp.CallToolResult {
	if !strings.HasSuffix(file, ".go") {
		return errorResult(fmt.Sprintf("rejected: file %q is not a Go file (.go extension required)", file))
	}
	return nil
}

// formatPatternResults renders pattern-mode symbol results as a compact list
// grouped by kind (methods / functions / types). When tokenBudget > 0, output
// is truncated with a trailer indicating how many results were omitted.
func formatPatternResults(results []domain.SymbolResult, showBody bool, pattern string, tokenBudget int) string {
	var methods, funcs, types []domain.SymbolResult
	for _, r := range results {
		switch {
		case r.Receiver != "":
			methods = append(methods, r)
		case strings.HasPrefix(r.Signature, "func"):
			funcs = append(funcs, r)
		default:
			types = append(types, r)
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d declaration(s) matching /%s/\n\n", len(results), pattern)

	writeGroup := func(title string, rs []domain.SymbolResult) {
		if len(rs) == 0 {
			return
		}
		fmt.Fprintf(&sb, "%s:\n", title)
		for _, r := range rs {
			if r.Receiver != "" {
				fmt.Fprintf(&sb, "- (%s) %s — %s:%d\n", r.Receiver, r.Name, r.File, r.LineStart)
			} else {
				fmt.Fprintf(&sb, "- %s — %s:%d\n", r.Name, r.File, r.LineStart)
			}
			if showBody {
				fmt.Fprintf(&sb, "  %s\n", r.Signature)
			}
		}
		sb.WriteByte('\n')
	}

	writeGroup("Methods", methods)
	writeGroup("Functions", funcs)
	writeGroup("Types", types)

	if tokenBudget > 0 {
		approx := len(sb.String()) / 4
		if approx > tokenBudget {
			return truncateToBudget(sb.String(), tokenBudget, len(results))
		}
	}
	return sb.String()
}

// truncateToBudget clips output to roughly tokenBudget tokens (~4 chars/token).
func truncateToBudget(s string, tokenBudget, total int) string {
	limit := tokenBudget * 4
	if limit >= len(s) {
		return s
	}
	cut := strings.LastIndexByte(s[:limit], '\n')
	if cut < 0 {
		cut = limit
	}
	return s[:cut] + fmt.Sprintf("\n... (truncated to fit token_budget=%d; total %d results)\n", tokenBudget, total)
}

// symbolEdit describes one symbol operation within an edit result.
type symbolEdit struct {
	Action     string `json:"action"`
	Identifier string `json:"identifier,omitempty"`
	File       string `json:"file"`
}

// editOutput is the structured result for all write tools (create, update,
// delete, insert_call, execute_plan, add/update/delete_interface).
type editOutput struct {
	FilesModified []string     `json:"files_modified"`
	Symbols       []symbolEdit `json:"symbols,omitempty"`
	Warnings      []string     `json:"warnings,omitempty"`
	AddedImports  []string     `json:"added_imports,omitempty"`
}

// patchFileOutput is the structured result for patch_file.
type patchFileOutput struct {
	File         string   `json:"file"`
	Applied      int      `json:"applied"`
	Hits         []int    `json:"hits,omitempty"`
	Preview      bool     `json:"preview,omitempty"`
	Diff         string   `json:"diff,omitempty"`
	AddedImports []string `json:"added_imports,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

// patchOutput is the structured result for patch_function, patch_struct and patch_interface.
type patchOutput struct {
	File         string   `json:"file"`
	Identifier   string   `json:"identifier"`
	Applied      int      `json:"applied"`
	Preview      bool     `json:"preview,omitempty"`
	Diff         string   `json:"diff,omitempty"`
	MockUpdated  bool     `json:"mock_updated,omitempty"`
	AddedImports []string `json:"added_imports,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

// implementOutput is the structured result for the implement tool.
type implementOutput struct {
	File      string   `json:"file"`
	Interface string   `json:"interface"`
	Receiver  string   `json:"receiver"`
	Stubs     []string `json:"stubs_added"`
}

// mockOutput is the structured result for the mock tool.
type mockOutput struct {
	File      string `json:"file"`
	Interface string `json:"interface"`
	MockName  string `json:"mock_name"`
}

// testOutput is the structured result for the test tool.
type testOutput struct {
	TestFile   string `json:"test_file"`
	Identifier string `json:"identifier"`
}

// tagOutput is the structured result for the tag tool.
type tagOutput struct {
	File       string `json:"file"`
	Identifier string `json:"identifier"`
	Field      string `json:"field,omitempty"`
}

// extractInterfaceOutput is the structured result for the extract_interface tool.
type extractInterfaceOutput struct {
	InterfaceName string `json:"interface_name"`
	InterfaceFile string `json:"interface_file"`
	MockFile      string `json:"mock_file,omitempty"`
	MockName      string `json:"mock_name,omitempty"`
}
