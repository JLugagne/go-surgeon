package commands

import (
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/spf13/cobra"
)

func NewScaffoldCommand(scaffolder service.ScaffolderCommands) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scaffold [command] [--param value ...]",
		Short: "Run a scaffolding template command, or list available commands",
		Long: `Executes a named scaffolding template command with the given parameters.
With no argument, lists all available scaffolding commands and their parameters.

Scaffolding commands and their parameters are defined in YAML manifests under
.go-surgeon/scaffold/ in the project root. Each manifest describes a template that
generates one or more files from Go text/template syntax.

Parameters are passed as --key value pairs after the command name. Key names are
capitalized automatically for use in templates ({{.Name}}).`,
		Example: `  # List all available scaffolding commands and their parameters
  go-surgeon scaffold

  # Run a scaffolding command
  go-surgeon scaffold catalog --name orders --module github.com/myorg/myapp

  # Same via the "list" alias
  go-surgeon list`,
		Aliases:            []string{"list"},
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			// Handle --help / -h manually since flag parsing is disabled.
			if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
				manifest, err := scaffolder.GetManifest(ctx)
				if err != nil {
					return err
				}
				fmt.Println("Available Scaffolding Commands:")
				for _, c := range manifest.Commands {
					fmt.Printf("\n- %s: %s\n", c.Name, c.Description)
					for _, param := range c.Parameters {
						fmt.Printf("    --%s : %s\n", param.Name, param.Description)
					}
				}
				return nil
			}

			commandName := args[0]
			params := make(map[string]string)
			for i := 1; i < len(args); i++ {
				arg := args[i]
				if strings.HasPrefix(arg, "--") {
					key := strings.TrimPrefix(arg, "--")
					if strings.Contains(key, "=") {
						parts := strings.SplitN(key, "=", 2)
						params[strings.Title(parts[0])] = parts[1]
					} else if i+1 < len(args) {
						params[strings.Title(key)] = args[i+1]
						i++
					}
				}
			}

			if err := scaffolder.Scaffold(ctx, commandName, params); err != nil {
				return err
			}
			fmt.Printf("SUCCESS: Scaffolded %s\n", commandName)
			return nil
		},
	}
	return cmd
}
