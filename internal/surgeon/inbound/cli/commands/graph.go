package commands

import (
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/spf13/cobra"
)

func NewGraphCommand(queries service.SurgeonQueries) *cobra.Command {
	var symbols bool
	var dir string

	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Print the package graph of the current project",
		Long: `Walks all Go packages under the current directory and prints their import paths.

With --symbols and --dir, also lists the exported types, functions, and methods defined
in each file under that subtree. --symbols requires --dir to prevent overwhelming output
on large projects.

Use "graph" for project orientation before editing code, and "graph --symbols --dir <path>"
to discover what symbols are available in a specific package area.`,
		Example: `  # List all packages in the project
  go-surgeon graph

  # List exported symbols in a package subtree
  go-surgeon graph --symbols --dir internal/catalog/domain

  # Short flags
  go-surgeon graph -s -d internal/catalog/domain`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if symbols && !cmd.Flags().Changed("dir") {
				return fmt.Errorf("--symbols requires --dir to scope the output")
			}
			packages, err := queries.Graph(ctx, dir, symbols)
			if err != nil {
				return fmt.Errorf("failed to build graph: %w", err)
			}
			if !symbols {
				for _, pkg := range packages {
					fmt.Println(pkg.Path)
				}
				return nil
			}
			first := true
			for _, pkg := range packages {
				for _, file := range pkg.Files {
					if !first {
						fmt.Println()
					}
					first = false
					fmt.Println(file.Path)
					for _, sym := range file.Symbols {
						for _, line := range strings.Split(sym, "\n") {
							fmt.Printf("  %s\n", line)
						}
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&symbols, "symbols", "s", false, "Include exported symbols per file")
	cmd.Flags().StringVarP(&dir, "dir", "d", ".", "Directory to walk")
	return cmd
}
