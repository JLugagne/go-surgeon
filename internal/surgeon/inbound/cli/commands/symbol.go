package commands

import (
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/spf13/cobra"
)

func NewSymbolCommand(queries service.SurgeonQueries) *cobra.Command {
	var showBody bool
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

If multiple matches are found, a disambiguation list is printed grouped by kind
(methods, functions, structs). Refine with Receiver.Name or scope with --dir.

Run this before editing a function — read the current body first.`,
		Example: `  # Find a function or struct by name
  go-surgeon symbol NewBook

  # Find a specific method on a receiver
  go-surgeon symbol BookHandler.Handle

  # Print the full body with line numbers
  go-surgeon symbol NewBook --body

  # Scope the search to a directory
  go-surgeon symbol Validate --dir internal/catalog/domain

  # Typical workflow: read before you edit
  go-surgeon symbol BookHandler.Handle --body
  cat <<'EOF' | go-surgeon update-func --file internal/catalog/inbound/http/handler.go --id BookHandler.Handle
  func (h *BookHandler) Handle(ctx context.Context, cmd CreateBookCommand) error {
    // new implementation
  }
  EOF

  # Short flags
  go-surgeon symbol BookHandler.Handle -b -d internal/catalog`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			queryStr := args[0]
			var query domain.SymbolQuery
			parts := strings.SplitN(queryStr, ".", 2)
			if len(parts) == 2 {
				query.Receiver = parts[0]
				query.Name = parts[1]
			} else {
				query.Name = parts[0]
			}

			results, err := queries.FindSymbols(ctx, query, targetDir)
			if err != nil {
				return fmt.Errorf("failed to search symbols: %w", err)
			}

			if len(results) == 0 {
				fmt.Printf("No matches found for symbol '%s'.\n", queryStr)
				return nil
			}

			if len(results) > 1 {
				fmt.Printf("Found %d matches for symbol '%s'. Please refine your search:\n\n", len(results), queryStr)
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
				fmt.Printf("Tip: Refine with 'go-surgeon symbol Receiver.Method' or add '--dir path/to/dir'.\n")
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
	cmd.Flags().StringVarP(&targetDir, "dir", "d", ".", "Directory to search in")
	return cmd
}
