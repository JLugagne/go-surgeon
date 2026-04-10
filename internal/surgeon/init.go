package surgeon

import (
	"context"

	appcommands "github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	appqueries "github.com/JLugagne/go-surgeon/internal/surgeon/app/queries"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	clicommands "github.com/JLugagne/go-surgeon/internal/surgeon/inbound/cli/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/outbound/filesystem"
	"github.com/spf13/cobra"
)

// Runner is a function that executes the CLI logic.
type Runner func(ctx context.Context, args []string) error

func Setup() Runner {
	return func(ctx context.Context, args []string) error {
		realFS := filesystem.NewFileSystem()
		proxyFS := &filesystem.ProxyFileSystem{Active: realFS}

		executePlanHandler := appcommands.NewExecutePlanHandler(proxyFS)
		queriesHandler := appqueries.NewSurgeonQueriesHandler(proxyFS)

		rootCmd := &cobra.Command{
			Use:           "go-surgeon",
			Short:         "AST-based Go code editor",
			SilenceErrors: true,
			SilenceUsage:  true,
		}

		var dryRun bool
		var diffAlias bool
		rootCmd.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "Preview changes as unified diff instead of writing to disk")
		rootCmd.PersistentFlags().BoolVar(&diffAlias, "diff", false, "Alias for --dry-run")
		_ = rootCmd.PersistentFlags().MarkHidden("diff")

		var dryRunFS *filesystem.DryRunFileSystem

		rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
			if dryRun || diffAlias {
				dryRunFS = filesystem.NewDryRunFileSystem(realFS)
				proxyFS.Active = dryRunFS
			}
			return nil
		}

		rootCmd.PersistentPostRunE = func(cmd *cobra.Command, args []string) error {
			if (dryRun || diffAlias) && dryRunFS != nil {
				return dryRunFS.PrintDiffs(ctx)
			}
			return nil
		}

		// Query commands
		rootCmd.AddCommand(
			clicommands.NewGraphCommand(queriesHandler),
			clicommands.NewSymbolCommand(queriesHandler),
		)

		// Per-action subcommands (stdin = raw Go source, flags = metadata)
		rootCmd.AddCommand(
			clicommands.NewActionCommand(executePlanHandler, domain.ActionTypeCreateFile, "create-file", false, true),
			clicommands.NewActionCommand(executePlanHandler, domain.ActionTypeReplaceFile, "replace-file", false, true),
			clicommands.NewActionCommand(executePlanHandler, domain.ActionTypeAddFunc, "add-func", false, true),
			clicommands.NewActionCommand(executePlanHandler, domain.ActionTypeUpdateFunc, "update-func", true, true),
			clicommands.NewActionCommand(executePlanHandler, domain.ActionTypeDeleteFunc, "delete-func", true, false),
			clicommands.NewActionCommand(executePlanHandler, domain.ActionTypeAddStruct, "add-struct", false, true),
			clicommands.NewActionCommand(executePlanHandler, domain.ActionTypeUpdateStruct, "update-struct", true, true),
			clicommands.NewActionCommand(executePlanHandler, domain.ActionTypeDeleteStruct, "delete-struct", true, false),
		)

		// Interface subcommands with auto-mock generation
		rootCmd.AddCommand(
			clicommands.NewInterfaceCommand(executePlanHandler, domain.ActionTypeAddInterface, "add-interface"),
			clicommands.NewInterfaceCommand(executePlanHandler, domain.ActionTypeUpdateInterface, "update-interface"),
			clicommands.NewInterfaceCommand(executePlanHandler, domain.ActionTypeDeleteInterface, "delete-interface"),
		)

		// Other commands
		rootCmd.AddCommand(
			clicommands.NewImplementCommand(executePlanHandler),
			clicommands.NewMockCommand(executePlanHandler),
			clicommands.NewTestCommand(executePlanHandler),
			clicommands.NewTagCommand(executePlanHandler),
			clicommands.NewExtractInterfaceCommand(executePlanHandler),
			clicommands.NewExecutePlanCommand(executePlanHandler),
		)

		rootCmd.SetArgs(args)
		return rootCmd.ExecuteContext(ctx)
	}
}
