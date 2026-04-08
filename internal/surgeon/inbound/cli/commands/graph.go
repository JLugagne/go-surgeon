package commands

import (
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/spf13/cobra"
)

func NewGraphCommand(queries service.SurgeonQueries) *cobra.Command {
	var symbols bool
	var summary bool
	var deps bool
	var recursive bool
	var tests bool
	var dir string

	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Print the package graph of the current project",
		Long: `Walks all Go packages under the current directory and prints their import paths.

With --symbols and --dir, lists exported types, functions, and methods defined in the
target package only (non-recursive by default). Add --recursive to include sub-packages.
--symbols requires --dir to prevent overwhelming output on large projects.

With --tests, _test.go files are included in the output. Unexported symbols (test helpers,
setup functions) are shown for test files since they are the primary reason to read them.

With --summary, appends a one-line package description derived from the Go package comment.
With --deps, shows the internal import dependencies between packages (project-module only).

Use "graph" for project orientation, "graph -s -d <pkg>" for that package's symbols,
then "graph -s -d <sub-pkg>" to zoom in further.`,
		Example: `  # List all packages in the project
  go-surgeon graph

  # Symbols in one package only (non-recursive)
  go-surgeon graph --symbols --dir internal/catalog/domain

  # Include test files alongside production symbols
  go-surgeon graph --symbols --tests --dir internal/catalog/app/commands

  # Symbols in a package and all sub-packages
  go-surgeon graph --symbols --recursive --dir internal/catalog/domain

  # Show package summaries and internal dependency graph
  go-surgeon graph --summary --deps

  # Full architectural overview
  go-surgeon graph --summary --deps --symbols --dir internal/catalog`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if symbols && !cmd.Flags().Changed("dir") {
				return fmt.Errorf("--symbols requires --dir to scope the output")
			}
			packages, err := queries.Graph(ctx, dir, symbols, summary, deps, recursive, tests)
			if err != nil {
				return fmt.Errorf("failed to build graph: %w", err)
			}

			if !symbols {
				for _, pkg := range packages {
					line := pkg.Path
					if summary {
						line += " — " + pkg.Summary
					}
					if deps {
						if len(pkg.Deps) > 0 {
							line += " → " + strings.Join(pkg.Deps, ", ")
						} else {
							line += " → (none)"
						}
					}
					fmt.Println(line)
				}
				return nil
			}

			// Symbols view: packages with a header line when --summary or --deps is active.
			showHeader := summary || deps
			first := true
			for _, pkg := range packages {
				hasFiles := len(pkg.Files) > 0
				if !showHeader && !hasFiles {
					continue
				}

				if !first {
					fmt.Println()
				}
				first = false

				if showHeader {
					line := pkg.Path
					if summary {
						line += " — " + pkg.Summary
					}
					if deps {
						if len(pkg.Deps) > 0 {
							line += " → " + strings.Join(pkg.Deps, ", ")
						} else {
							line += " → (none)"
						}
					}
					fmt.Println(line)
				}

				for _, file := range pkg.Files {
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
	cmd.Flags().BoolVarP(&summary, "summary", "S", false, "Append package doc comment summary to each package")
	cmd.Flags().BoolVarP(&deps, "deps", "D", false, "Show internal package import dependencies")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "Walk sub-packages when --symbols is set (default: target dir only)")
	cmd.Flags().BoolVarP(&tests, "tests", "t", false, "Include _test.go files (shows unexported helpers too)")
	cmd.Flags().StringVarP(&dir, "dir", "d", ".", "Directory to walk")
	return cmd
}
