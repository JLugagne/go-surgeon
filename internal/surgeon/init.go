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
	fs := filesystem.NewFileSystem()

	executePlanHandler := appcommands.NewExecutePlanHandler(fs)
	scaffolderHandler := appcommands.NewScaffolderHandler(fs)
	queriesHandler := appqueries.NewSurgeonQueriesHandler(fs)

	return func(ctx context.Context, args []string) error {
		rootCmd := &cobra.Command{
			Use:           "go-surgeon",
			Short:         "AST-based Go code editor",
			SilenceErrors: true,
			SilenceUsage:  true,
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
			clicommands.NewScaffoldCommand(scaffolderHandler),
			clicommands.NewExecutePlanCommand(executePlanHandler),
		)

		rootCmd.SetArgs(args)
		return rootCmd.ExecuteContext(ctx)
	}
}
