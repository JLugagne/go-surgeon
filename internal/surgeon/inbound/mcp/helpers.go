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

	return sb.String()
}

// findSymbols mirrors the CLI symbol command's multi-form query resolution.
func findSymbols(ctx context.Context, queries service.SurgeonQueries, queryStr string, tests bool, dir string) []domain.SymbolResult {
	parts := strings.Split(queryStr, ".")
	var allResults []domain.SymbolResult

	if len(parts) == 1 {
		query := domain.SymbolQuery{Name: parts[0], Tests: tests}
		results, _ := queries.FindSymbols(ctx, query, dir)
		allResults = append(allResults, results...)
	}

	if len(parts) == 2 {
		query1 := domain.SymbolQuery{Receiver: parts[0], Name: parts[1], Tests: tests}
		results1, _ := queries.FindSymbols(ctx, query1, dir)
		allResults = append(allResults, results1...)

		query2 := domain.SymbolQuery{PackageName: parts[0], Name: parts[1], Tests: tests}
		results2, _ := queries.FindSymbols(ctx, query2, dir)
		allResults = append(allResults, results2...)
	}

	if len(parts) == 3 {
		query := domain.SymbolQuery{PackageName: parts[0], Receiver: parts[1], Name: parts[2], Tests: tests}
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
