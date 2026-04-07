package surgeon

import (
	"context"
	"fmt"

	appcommands "github.com/JLugagne/go-surgeon/internal/surgeon/app/commands"
	appqueries "github.com/JLugagne/go-surgeon/internal/surgeon/app/queries"
	clicommands "github.com/JLugagne/go-surgeon/internal/surgeon/inbound/cli/commands"
	"github.com/JLugagne/go-surgeon/internal/surgeon/outbound/filesystem"
)

// Runner is a function that executes the CLI logic.
type Runner func(ctx context.Context, args []string) error

// Setup instantiates the application components and returns a runner function.
func Setup() Runner {
	fs := filesystem.NewFileSystem()

	// Handlers
	executePlanHandler := appcommands.NewExecutePlanHandler(fs)
	scaffolderHandler := appcommands.NewScaffolderHandler(fs)
	queriesHandler := appqueries.NewSurgeonQueriesHandler(fs)

	// Commands
	executeCommand := clicommands.NewExecutePlanCommand(executePlanHandler)
	scaffoldCommand := clicommands.NewScaffoldCommand(scaffolderHandler)
	symbolCommand := clicommands.NewSymbolCommand(queriesHandler)
	implementCommand := clicommands.NewImplementCommand(executePlanHandler)

	return func(ctx context.Context, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("usage: go-surgeon <command> [args]\nAvailable commands: execute, scaffold, list, symbol, implement")
		}

		cmd := args[0]
		remainingArgs := args[1:]

		switch cmd {
		case "execute":
			return executeCommand.Run(ctx, remainingArgs)
		case "scaffold":
			return scaffoldCommand.Run(ctx, remainingArgs)
		case "list":
			// Call scaffold without args to print the manifest
			return scaffoldCommand.Run(ctx, []string{})
		case "symbol":
			return symbolCommand.Run(ctx, remainingArgs)
		case "implement":
			return implementCommand.Run(ctx, remainingArgs)
		default:
			return fmt.Errorf("unknown command: %s. Expected execute, scaffold, list, symbol, or implement", cmd)
		}
	}
}
