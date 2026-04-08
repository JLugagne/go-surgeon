package commands

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/spf13/cobra"
)

func NewSymbolCommand(queries service.SurgeonQueries) *cobra.Command {
	var showBody bool
	var tests bool
	var targetDir string

	cmd := &cobra.Command{
		Use:   "symbol [Receiver.]Name",
		Short: "Look up a symbol (function, method, or struct) by name",
		Long: `Searches all Go files under --dir for a function, method, or struct matching the query.

Query forms:
  "Name"           matches any function or struct named Name
  "Receiver.Name"  matches a method Name on receiver type Receiver

With --body, the full source code is printed with line numbers (empty lines stripped).
Without --body, only the declaration signature is shown.

By default, _test.go files are excluded. Use --tests to include test functions and
unexported helpers (e.g. setupTestApp, mockFS) that live in test files.

If multiple matches are found, a disambiguation list is printed grouped by kind
(methods, functions, structs). Refine with Receiver.Name or scope with --dir.

Run this before editing a function — read the current body first.`,
		Example: `  # Find a function or struct by name
  go-surgeon symbol NewBook

  # Find a specific method on a receiver
  go-surgeon symbol BookHandler.Handle

  # Print the full body with line numbers
  go-surgeon symbol NewBook --body

  # Find a test helper or test function
  go-surgeon symbol setupTestApp --tests --body --dir internal/catalog/app/commands

  # Batch read: helper + one example test (saves a full-file Read)
  go-surgeon symbol "setupTestApp|TestCreateBook_Success" -t -b -d internal/catalog/app/commands

  # Scope the search to a directory
  go-surgeon symbol Validate --dir internal/catalog/domain

  # Short flags
  go-surgeon symbol BookHandler.Handle -b -d internal/catalog`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			queryStr := args[0]
			
			var allResults []domain.SymbolResult
			
			parts := strings.Split(queryStr, ".")
			
			// 1. Exact Name: "MyFunc"
			if len(parts) == 1 {
				query := domain.SymbolQuery{Name: parts[0], Tests: tests}
				results, _ := queries.FindSymbols(ctx, query, targetDir)
				allResults = append(allResults, results...)
			}
			
			// 2. Two parts: "pkg.Func" or "Receiver.Method"
			if len(parts) == 2 {
				// Try Receiver.Method
				query1 := domain.SymbolQuery{Receiver: parts[0], Name: parts[1], Tests: tests}
				results1, _ := queries.FindSymbols(ctx, query1, targetDir)
				allResults = append(allResults, results1...)
				
				// Try pkg.Name
				query2 := domain.SymbolQuery{PackageName: parts[0], Name: parts[1], Tests: tests}
				results2, _ := queries.FindSymbols(ctx, query2, targetDir)
				allResults = append(allResults, results2...)
			}
			
			// 3. Three parts: "pkg.Receiver.Method"
			if len(parts) == 3 {
				query := domain.SymbolQuery{PackageName: parts[0], Receiver: parts[1], Name: parts[2], Tests: tests}
				results, _ := queries.FindSymbols(ctx, query, targetDir)
				allResults = append(allResults, results...)
			}

			results := allResults
			if len(results) == 0 {
				fmt.Printf("No matches found for '%s'.\n", queryStr)
				fmt.Printf("Hint: run 'go-surgeon graph -s -d %s' to list available symbols, or check the Receiver.Method format.\n", targetDir)
				return nil
			}

			if len(results) > 1 {
				fmt.Printf("Found %d matches for '%s'. Please refine your search:\n\n", len(results), queryStr)
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
					fmt.Println("Matches (Methods):")
					for _, r := range methods {
						fmt.Printf("- (%s) %s in %s:%d\n", r.Receiver, r.Name, r.File, r.LineStart)
					}
					fmt.Println()
				}
				if len(funcs) > 0 {
					fmt.Println("Matches (Functions):")
					for _, r := range funcs {
						fmt.Printf("- %s in %s:%d\n", r.Name, r.File, r.LineStart)
					}
					fmt.Println()
				}
				if len(structs) > 0 {
					fmt.Println("Matches (Structs):")
					for _, r := range structs {
						fmt.Printf("- %s in %s:%d\n", r.Name, r.File, r.LineStart)
					}
					fmt.Println()
				}
				// Build a concrete example from the first available match.
				var hintCmd string
				if len(methods) > 0 {
					first := methods[0]
					hintCmd = fmt.Sprintf("go-surgeon symbol %s.%s", first.Receiver, first.Name)
				} else if len(funcs) > 0 {
					first := funcs[0]
					hintCmd = fmt.Sprintf("go-surgeon symbol %s --dir %s", first.Name, filepath.Dir(first.File))
				} else {
					first := structs[0]
					hintCmd = fmt.Sprintf("go-surgeon symbol %s --dir %s", first.Name, filepath.Dir(first.File))
				}
				fmt.Printf("Hint: refine with '%s', or scope with '--dir <path>'.\n", hintCmd)
				return nil
			}

			res := results[0]
			fmt.Printf("Symbol: %s\n", res.Name)
			if res.Receiver != "" {
				fmt.Printf("Receiver: %s\n", res.Receiver)
			}
			bodyLines := res.LineEnd - res.LineStart + 1
			fmt.Printf("File: %s:%d-%d (%d lines body)\n", res.File, res.LineStart, res.LineEnd, bodyLines)
			if res.Doc != "" {
				fmt.Printf("Doc:\n%s\n", res.Doc)
			}
			if showBody {
				fmt.Printf("Code (Empty lines stripped):\n%s\n", res.Code)
			} else {
				fmt.Printf("Signature:\n%s\n", res.Signature)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&showBody, "body", "b", false, "Show the full function/struct body")
	cmd.Flags().BoolVarP(&tests, "tests", "t", false, "Include _test.go files in the search")
	cmd.Flags().StringVarP(&targetDir, "dir", "d", ".", "Directory to search in")
	return cmd
}
