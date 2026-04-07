package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
)

// ScaffoldCommand handles the dynamic scaffolding execution.
type ScaffoldCommand struct {
	scaffolder service.ScaffolderCommands
}

// NewScaffoldCommand creates a new ScaffoldCommand.
func NewScaffoldCommand(scaffolder service.ScaffolderCommands) *ScaffoldCommand {
	return &ScaffoldCommand{scaffolder: scaffolder}
}

// Run executes the scaffold command logic.
func (c *ScaffoldCommand) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return c.printManifest(ctx)
	}

	commandName := args[0]
	params := make(map[string]string)

	// Basic flag parsing: --name catalog
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--") {
			key := strings.TrimPrefix(arg, "--")
			if strings.Contains(key, "=") {
				parts := strings.SplitN(key, "=", 2)
				params[strings.Title(parts[0])] = parts[1] // Capitalize for template access {{.Name}}
			} else if i+1 < len(args) {
				params[strings.Title(key)] = args[i+1]
				i++ // Skip next arg
			}
		}
	}

	err := c.scaffolder.Scaffold(ctx, commandName, params)
	if err != nil {
		return err
	}
	
	fmt.Printf("SUCCESS: Scaffolded %s\n", commandName)
	return nil
}

// printManifest lists available scaffolding commands.
func (c *ScaffoldCommand) printManifest(ctx context.Context) error {
	manifest, err := c.scaffolder.GetManifest(ctx)
	if err != nil {
		return err
	}
	fmt.Println("Available Scaffolding Commands:")
	for _, cmd := range manifest.Commands {
		fmt.Printf("\n- %s: %s\n", cmd.Name, cmd.Description)
		for _, param := range cmd.Parameters {
			fmt.Printf("    --%s : %s\n", param.Name, param.Description)
		}
	}
	return nil
}
