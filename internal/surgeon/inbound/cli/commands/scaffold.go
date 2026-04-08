package commands

import (
	"fmt"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/spf13/cobra"
)

func NewScaffoldCommand(scaffolder service.ScaffolderCommands) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scaffold",
		Short: "Template-driven project orchestration",
		Long: `Scaffold manages template-driven project orchestration.
Templates are stored in .surgeon-templates/<template-name>/manifest.yaml
and can contain commands, variables, hints, and post_commands for workflow chaining.`,
		Aliases: []string{"list"},
	}

	cmd.AddCommand(newListTemplatesCommand(scaffolder))
	cmd.AddCommand(newDocCommand(scaffolder))
	cmd.AddCommand(newExecuteCommand(scaffolder))

	return cmd
}

func newListTemplatesCommand(scaffolder service.ScaffolderCommands) *cobra.Command {
	return &cobra.Command{
		Use:   "list-templates",
		Short: "List all available scaffolding templates",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			templates, err := scaffolder.ListTemplates(ctx)
			if err != nil {
				return err
			}

			if len(templates) == 0 {
				fmt.Println("No templates found in .surgeon-templates/")
				return nil
			}

			fmt.Println("Available Templates:")
			for _, tmpl := range templates {
				fmt.Printf("\n- %s:\n", tmpl.Name)
				desc := strings.TrimSpace(tmpl.Description)
				if desc != "" {
					for _, line := range strings.Split(desc, "\n") {
						fmt.Printf("    %s\n", line)
					}
				}
			}
			return nil
		},
	}
}

func newDocCommand(scaffolder service.ScaffolderCommands) *cobra.Command {
	return &cobra.Command{
		Use:   "doc <template> [command]",
		Short: "Show documentation for a template or specific command",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			tmpl, err := scaffolder.GetTemplate(ctx, args[0])
			if err != nil {
				return err
			}

			if len(args) == 1 {
				fmt.Printf("Template: %s\n", tmpl.Name)
				fmt.Printf("%s\n\n", tmpl.Description)
				fmt.Println("Commands:")
				for _, c := range tmpl.Commands {
					fmt.Printf("  %s - %s\n", c.Command, c.Description)
				}
				fmt.Printf("\nRun 'go-surgeon scaffold doc %s <command>' for details.\n", tmpl.Name)
				return nil
			}

			commandName := args[1]
			cmdMap := make(map[string]domain.TemplateCommand)
			for _, c := range tmpl.Commands {
				cmdMap[c.Command] = c
			}

			targetCmd, ok := cmdMap[commandName]
			if !ok {
				return fmt.Errorf("command '%s' not found in template '%s'", commandName, tmpl.Name)
			}

			fmt.Printf("Command: %s\n", targetCmd.Command)
			fmt.Printf("  %s\n\n", targetCmd.Description)

			// Tree walk for deduplicated variables and execution path
			var executionPath []string
			visited := make(map[string]bool)
			var variables []domain.TemplateVariable
			varKeys := make(map[string]bool)

			var walk func(cmdName string)
			walk = func(cmdName string) {
				if visited[cmdName] {
					return
				}
				visited[cmdName] = true
				executionPath = append(executionPath, cmdName)

				c := cmdMap[cmdName]
				for _, v := range c.Variables {
					if !varKeys[v.Key] {
						varKeys[v.Key] = true
						variables = append(variables, v)
					}
				}

				for _, postCmd := range c.PostCommands {
					walk(postCmd)
				}
			}

			walk(commandName)

			if len(executionPath) > 1 {
				fmt.Printf("  Executes: %s\n\n", strings.Join(executionPath, " \u2192 "))
			}

			if len(variables) > 0 {
				fmt.Println("  Variables (all commands, deduplicated):")
				for _, v := range variables {
					fmt.Printf("    --set %s\t%s\n", v.Key, v.Description)
				}
			} else {
				fmt.Println("  Variables: None")
			}

			return nil
		},
	}
}

func newExecuteCommand(scaffolder service.ScaffolderCommands) *cobra.Command {
	var sets []string

	cmd := &cobra.Command{
		Use:   "execute <template> <command> [--set Key=Value ...]",
		Short: "Execute a template command",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			templateName := args[0]
			commandName := args[1]

			tmpl, err := scaffolder.GetTemplate(ctx, templateName)
			if err != nil {
				return err
			}

			cmdMap := make(map[string]domain.TemplateCommand)
			for _, c := range tmpl.Commands {
				cmdMap[c.Command] = c
			}

			if _, ok := cmdMap[commandName]; !ok {
				return fmt.Errorf("command '%s' not found in template '%s'", commandName, templateName)
			}

			// Tree walk for deduplicated variables and execution path
			visited := make(map[string]bool)
			var variables []domain.TemplateVariable
			varKeys := make(map[string]bool)

			var walk func(cmdName string)
			walk = func(cmdName string) {
				if visited[cmdName] {
					return
				}
				visited[cmdName] = true
				c := cmdMap[cmdName]
				for _, v := range c.Variables {
					if !varKeys[v.Key] {
						varKeys[v.Key] = true
						variables = append(variables, v)
					}
				}

				for _, postCmd := range c.PostCommands {
					walk(postCmd)
				}
			}

			walk(commandName)

			params := make(map[string]string)
			for _, set := range sets {
				parts := strings.SplitN(set, "=", 2)
				if len(parts) == 2 {
					params[parts[0]] = parts[1]
				}
			}

			var missing []domain.TemplateVariable
			for _, v := range variables {
				if _, ok := params[v.Key]; !ok {
					missing = append(missing, v)
				}
			}

			if len(missing) > 0 {
				fmt.Printf("ERROR: missing required variables for %s/%s:\n\n", templateName, commandName)
				for _, v := range missing {
					fmt.Printf("  --set %s\t%s\n", v.Key, v.Description)
				}
				fmt.Printf("\nUsage: go-surgeon scaffold execute %s %s", templateName, commandName)
				for _, v := range variables {
					fmt.Printf(" --set %s=\"value\"", v.Key)
				}
				fmt.Println()
				return fmt.Errorf("missing required variables")
			}

			return scaffolder.Execute(ctx, templateName, commandName, params)
		},
	}

	cmd.Flags().StringSliceVar(&sets, "set", nil, "Set variable values (e.g. AppName=catalog)")
	return cmd
}
