package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
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
	var depth int
	var focus string
	var exclude []string
	var tokenBudget int
	var module string

	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Print the package graph of the current project",
		Long: `Walks all Go packages under the current directory and prints their import paths.

With --symbols and --dir, lists exported types, functions, and methods defined in the
target package only (non-recursive by default). Add --recursive to include sub-packages.
--symbols requires --dir (or --focus or --module) to prevent overwhelming output.

With --tests, _test.go files are included in the output. Unexported symbols (test helpers,
setup functions) are shown for test files since they are the primary reason to read them.

With --summary, appends a one-line package description derived from the Go package comment.
With --deps, shows the internal import dependencies between packages (project-module only).

With --module IMPORTPATH, explore a third-party dependency instead of the current project.
The module must be listed in go.mod; its pinned version from go.mod is used automatically.
--dir and --focus are interpreted relative to the module root when --module is set.

Context window management flags:
  --depth N        Limit directory recursion depth (1 = target dir only, 2 = immediate children)
  --focus PATH     Full detail for focused package; path-only for everything else
  --exclude GLOB   Skip directories matching pattern (repeatable)
  --token-budget N Truncate output to fit approximate token count

Use "graph" for project orientation, "graph -s -d <pkg>" for that package's symbols,
then "graph --focus <pkg>" to zoom in on a specific package with full context.`,
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
  go-surgeon graph --summary --deps --symbols --dir internal/catalog

  # Limit recursion depth to 2 levels
  go-surgeon graph --summary --depth 2

  # Focus on a single package with full detail, path-only for the rest
  go-surgeon graph --summary --symbols --focus internal/catalog/domain

  # Exclude vendor and legacy directories
  go-surgeon graph --exclude vendor --exclude "*legacy*"

  # Fit output within ~2000 tokens
  go-surgeon graph --summary --deps --token-budget 2000

  # Explore a dependency's package structure
  go-surgeon graph --module github.com/spf13/cobra

  # Symbols in a dependency sub-package
  go-surgeon graph --symbols --module github.com/spf13/cobra --dir doc

  # Focus on one package within a dependency
  go-surgeon graph --module github.com/spf13/cobra --focus cobra`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if symbols && !cmd.Flags().Changed("dir") && focus == "" && module == "" {
				return fmt.Errorf("--symbols requires --dir (or --focus) to scope the output")
			}

			opts := domain.GraphOptions{
				Dir:         dir,
				Symbols:     symbols,
				Summary:     summary,
				Deps:        deps,
				Recursive:   recursive,
				Tests:       tests,
				Depth:       depth,
				Focus:       focus,
				Exclude:     exclude,
				TokenBudget: tokenBudget,
				Module:      module,
			}

			// --focus implies symbols + summary for the focused package.
			if focus != "" {
				opts.Symbols = true
				opts.Summary = true
				opts.Recursive = true
			}

			packages, err := queries.Graph(ctx, opts)
			if err != nil {
				return fmt.Errorf("failed to build graph: %w", err)
			}

			if !opts.Symbols {
				if len(packages) == 0 {
					fmt.Printf("No Go packages found in '%s'.\n", dir)
					fmt.Printf("Hint: check that you're in the project root, or use '--dir <path>' to target a subdirectory.\n")
					return nil
				}
				for _, pkg := range packages {
					line := pkg.Path
					if pkg.Summary != "" {
						line += " — " + pkg.Summary
					}
					if len(pkg.Deps) > 0 {
						line += " → " + strings.Join(pkg.Deps, ", ")
					} else if deps {
						line += " → (none)"
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
				hasHeader := showHeader && (pkg.Summary != "" || len(pkg.Deps) > 0)
				if !hasHeader && !hasFiles {
					// In focus mode, still print unfocused package paths.
					if focus != "" {
						fmt.Println(pkg.Path)
						first = false
						continue
					}
					continue
				}

				if !first {
					fmt.Println()
				}
				first = false

				if showHeader {
					line := pkg.Path
					if pkg.Summary != "" {
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

			// When non-recursive symbols mode (and no focus), hint at direct sub-packages.
			if !recursive && focus == "" {
				subPkgs := findDirectSubPackages(dir)
				if len(subPkgs) > 0 {
					fmt.Printf("\nSub-packages (use -r to include): %s\n", strings.Join(subPkgs, ", "))
				} else if first {
					fmt.Printf("No Go files found in '%s'.\n", dir)
					fmt.Printf("Hint: use '--dir <path>' to target a package directory, or '-r' to walk sub-packages.\n")
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
	cmd.Flags().IntVar(&depth, "depth", 0, "Limit directory recursion depth (0 = unlimited)")
	cmd.Flags().StringVar(&focus, "focus", "", "Package path for full detail; others show path only")
	cmd.Flags().StringArrayVar(&exclude, "exclude", nil, "Glob patterns for directories to skip (repeatable)")
	cmd.Flags().IntVar(&tokenBudget, "token-budget", 0, "Approximate max tokens in output (0 = unlimited)")
	cmd.Flags().StringVar(&module, "module", "", "Import path of a dependency to explore instead of the current project (e.g. github.com/spf13/cobra)")
	return cmd
}

// findDirectSubPackages returns the names of direct child directories of dir
// that contain at least one .go file (non-test files suffice).
func findDirectSubPackages(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var result []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		subDir := filepath.Join(dir, entry.Name())
		subEntries, err := os.ReadDir(subDir)
		if err != nil {
			continue
		}
		for _, sub := range subEntries {
			if !sub.IsDir() && strings.HasSuffix(sub.Name(), ".go") {
				result = append(result, entry.Name())
				break
			}
		}
	}
	sort.Strings(result)
	return result
}

type Dummy struct{}
