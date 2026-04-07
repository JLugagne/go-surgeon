package commands

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
)

// SymbolCommand handles the CLI querying for AST symbols.
type SymbolCommand struct {
	queries service.SurgeonQueries
}

// NewSymbolCommand creates a new SymbolCommand.
func NewSymbolCommand(queries service.SurgeonQueries) *SymbolCommand {
	return &SymbolCommand{queries: queries}
}

// Run executes the symbol command.
func (c *SymbolCommand) Run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("symbol", flag.ContinueOnError)
	showBody := fs.Bool("body", false, "Show the full function/struct body")
	targetDir := fs.String("dir", ".", "Directory to search in")

	var posArgs []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return err
		}
		if fs.NArg() > 0 {
			posArgs = append(posArgs, fs.Arg(0))
			args = fs.Args()[1:]
		} else {
			break
		}
	}
	if len(posArgs) < 1 {
		return fmt.Errorf("usage: symbol <[Receiver.]Name> [--body] [--dir <path>]")
	}

	queryStr := posArgs[0]
	var query domain.SymbolQuery
	parts := strings.SplitN(queryStr, ".", 2)
	if len(parts) == 2 {
		query.Receiver = parts[0]
		query.Name = parts[1]
	} else {
		query.Name = parts[0]
	}

	results, err := c.queries.FindSymbols(ctx, query, *targetDir)
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
		fmt.Printf("Tip: Use a more specific query like 'go-surgeon symbol Receiver.Method' or filter by directory '--dir path/to/dir'.\n")
		return nil
	}

	// Exactly 1 match
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
	
	if *showBody {
		fmt.Printf("Code (Empty lines stripped):\n%s\n", res.Code)
	} else {
		fmt.Printf("Signature:\n%s\n", res.Signature)
	}

	return nil
}
