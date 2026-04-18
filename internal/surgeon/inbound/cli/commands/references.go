package commands

import (
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/spf13/cobra"
)

// NewFindDefinitionCommand wires the find-definition CLI subcommand.
// It's a read-only lookup that prints file:line:column of the symbol's
// declaration — useful when grep gives too many matches or when the
// caller wants a type-aware resolution.
func NewFindDefinitionCommand(queries service.SurgeonQueries) *cobra.Command {
	var receiver, pkg, file, dir string
	var line int
	var tests bool

	cmd := &cobra.Command{
		Use:   "find-definition NAME",
		Short: "Locate the declaration of a Go symbol (type-aware)",
		Long: `Resolves the symbol via go/packages and prints the file:line:column
of its declaration. Pair NAME with --receiver / --package / --file / --line
to disambiguate when the name is not unique.`,
		Example: `  go-surgeon find-definition BookRepository
  go-surgeon find-definition Handle --receiver BookHandler
  go-surgeon find-definition Config --package internal/surgeon/domain`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			result, err := queries.FindDefinition(ctx, domain.ReferencesQuery{
				Symbol: domain.SymbolRef{
					Name:     args[0],
					Receiver: receiver,
					Package:  pkg,
					File:     file,
					Line:     line,
				},
				Dir:   dir,
				Tests: tests,
			})
			if err != nil {
				return err
			}
			if result.Definition.File == "" {
				fmt.Printf("No definition found for %q.\n", args[0])
				return nil
			}
			fmt.Printf("%s (%s) — %s:%d:%d\n", describeRefSymbol(result), result.Kind, result.Definition.File, result.Definition.Line, result.Definition.Column)
			if txt := strings.TrimSpace(result.Definition.LineText); txt != "" {
				fmt.Printf("  %s\n", txt)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&receiver, "receiver", "", "Receiver type name for methods")
	cmd.Flags().StringVar(&pkg, "package", "", "Package path or name for disambiguation")
	cmd.Flags().StringVar(&file, "file", "", "File path to pin an exact declaration")
	cmd.Flags().IntVar(&line, "line", 0, "Declaration line (1-based); pair with --file")
	cmd.Flags().StringVarP(&dir, "dir", "d", ".", "Directory to load packages from")
	cmd.Flags().BoolVarP(&tests, "tests", "t", false, "Include _test.go files in the search")
	return cmd
}

// NewFindReferencesCommand wires the find-references CLI subcommand.
// Prints each site the type-checker attributes to the resolved symbol.
func NewFindReferencesCommand(queries service.SurgeonQueries) *cobra.Command {
	var receiver, pkg, file, dir string
	var line int
	var tests, includeDef bool

	cmd := &cobra.Command{
		Use:   "find-references NAME",
		Short: "Find every reference to a Go symbol across the module",
		Long: `Loads the module via go/packages and prints file:line:column for every
identifier that resolves to the named symbol. Use --include-definition to
also print the declaration.`,
		Example: `  go-surgeon find-references BookRepository
  go-surgeon find-references Handle --receiver BookHandler --include-definition
  go-surgeon find-references helper --tests`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			result, err := queries.FindReferences(ctx, domain.ReferencesQuery{
				Symbol: domain.SymbolRef{
					Name:     args[0],
					Receiver: receiver,
					Package:  pkg,
					File:     file,
					Line:     line,
				},
				Dir:               dir,
				Tests:             tests,
				IncludeDefinition: includeDef,
			})
			if err != nil {
				return err
			}
			fmt.Printf("%s (%s): %d reference(s)\n", describeRefSymbol(result), result.Kind, len(result.References))
			if includeDef && result.Definition.File != "" {
				fmt.Printf("  def  %s:%d:%d\n", result.Definition.File, result.Definition.Line, result.Definition.Column)
			}
			for _, loc := range result.References {
				text := strings.TrimSpace(loc.LineText)
				if text == "" {
					fmt.Printf("  ref  %s:%d:%d\n", loc.File, loc.Line, loc.Column)
					continue
				}
				fmt.Printf("  ref  %s:%d:%d  %s\n", loc.File, loc.Line, loc.Column, text)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&receiver, "receiver", "", "Receiver type name for methods")
	cmd.Flags().StringVar(&pkg, "package", "", "Package path or name for disambiguation")
	cmd.Flags().StringVar(&file, "file", "", "File path to pin an exact declaration")
	cmd.Flags().IntVar(&line, "line", 0, "Declaration line (1-based); pair with --file")
	cmd.Flags().StringVarP(&dir, "dir", "d", ".", "Directory to load packages from")
	cmd.Flags().BoolVarP(&tests, "tests", "t", false, "Include _test.go files in the search")
	cmd.Flags().BoolVar(&includeDef, "include-definition", false, "Also print the definition site")
	return cmd
}

// NewRenameSymbolCommand wires the rename-symbol CLI subcommand.
// Rewrites every file where the named symbol is defined or used. Use
// --preview to inspect the diff before committing.
func NewRenameSymbolCommand(commands service.SurgeonCommands) *cobra.Command {
	var receiver, pkg, file, dir string
	var line int
	var tests, preview bool

	cmd := &cobra.Command{
		Use:   "rename-symbol OLD NEW",
		Short: "Rename a Go symbol and every reference to it across the module",
		Long: `Type-aware rename powered by go/packages. Only identifiers that resolve
to the same declaration are rewritten, so there are no false positives.

Refuses renames that change export status or collide with an existing
name in the same scope. Use --preview to list affected sites without
writing.`,
		Example: `  go-surgeon rename-symbol BookRepo BookRepository
  go-surgeon rename-symbol Handle Serve --receiver BookHandler
  go-surgeon rename-symbol OldName NewName --preview`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			result, err := commands.Rename(ctx, domain.RenameRequest{
				Symbol: domain.SymbolRef{
					Name:     args[0],
					Receiver: receiver,
					Package:  pkg,
					File:     file,
					Line:     line,
				},
				NewName: args[1],
				Dir:     dir,
				Tests:   tests,
				DryRun:  preview,
			})
			if err != nil {
				return err
			}
			verb := "Renamed"
			if result.DryRun {
				verb = "Would rename"
			}
			fmt.Printf("%s %s %q → %q: %d site(s) across %d file(s)\n", verb, result.Kind, result.OldName, result.NewName, len(result.Locations), len(result.FilesModified))
			for _, f := range result.FilesModified {
				count := 0
				for _, l := range result.Locations {
					if l.File == f {
						count++
					}
				}
				fmt.Printf("  %s: %d\n", f, count)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&receiver, "receiver", "", "Receiver type name for methods")
	cmd.Flags().StringVar(&pkg, "package", "", "Package path or name for disambiguation")
	cmd.Flags().StringVar(&file, "file", "", "File path to pin an exact declaration")
	cmd.Flags().IntVar(&line, "line", 0, "Declaration line (1-based); pair with --file")
	cmd.Flags().StringVarP(&dir, "dir", "d", ".", "Directory to load packages from")
	cmd.Flags().BoolVarP(&tests, "tests", "t", false, "Include _test.go files in the rename")
	cmd.Flags().BoolVar(&preview, "preview", false, "List affected sites without writing any file")
	return cmd
}

// describeRefSymbol formats a SymbolRef for human output. It matches
// the conventions used by the symbol command so the CLI feels
// consistent: "(Receiver).Method", "pkg.Name", or bare "Name".
func describeRefSymbol(r domain.ReferencesResult) string {
	if r.Symbol.Receiver != "" {
		return fmt.Sprintf("(%s).%s", r.Symbol.Receiver, r.Symbol.Name)
	}
	if r.Symbol.Package != "" {
		return fmt.Sprintf("%s.%s", r.Symbol.Package, r.Symbol.Name)
	}
	return r.Symbol.Name
}
