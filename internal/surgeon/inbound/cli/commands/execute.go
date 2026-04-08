package commands

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/JLugagne/go-surgeon/internal/surgeon/inbound/converters"
	"github.com/spf13/cobra"
)

func NewExecutePlanCommand(surgeon service.SurgeonCommands) *cobra.Command {
	var files []string
	var keep bool

	cmd := &cobra.Command{
		Use:        "execute [plan.yaml]",
		Short:      "Execute a YAML plan file (deprecated: use individual subcommands)",
		Deprecated: "use individual subcommands instead (add-func, update-struct, etc.)",
		Long: `Reads a YAML plan file (or stdin) and executes all listed actions in order.

This command is deprecated. Use the individual subcommands (add-func, update-func,
delete-func, add-struct, etc.) instead — they provide clearer error messages, better
help, and are easier to use from scripts and LLM agents.

Plan file schema:
  actions:
    - action: create_file | replace_file
              add_func | update_func | delete_func
              add_struct | update_struct | delete_struct
              add_interface | update_interface | delete_interface
      file:       <target file path>
      identifier: <FuncName or Receiver.Method, for update/delete>
      content: |
        <raw Go source, no package declaration or imports>
      mock_file: <mock output path, for add/update_interface>
      mock_name: <mock struct name, for add/update_interface>

Maximum 5 actions per plan file. Total actions across all files is unlimited.`,
		Example: `  # Execute a plan from a file
  go-surgeon execute plan.yaml

  # Execute multiple plan files in one call (auto-cleanup on success)
  go-surgeon execute -f /tmp/plan1.yaml -f /tmp/plan2.yaml -f /tmp/plan3.yaml

  # Keep plan files even on success
  go-surgeon execute -f /tmp/plan1.yaml -f /tmp/plan2.yaml --keep

  # Execute a plan from stdin
  cat <<'EOF' | go-surgeon execute
  actions:
    - action: add_func
      file: internal/catalog/domain/book.go
      content: |
        func NewBook(title string) *Book {
          return &Book{Title: title}
        }
  EOF`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			if len(files) > 0 {
				totalFilesModified := 0
				for i, filePath := range files {
					data, err := os.ReadFile(filePath)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Plan files retained for debugging: %s\n", strings.Join(files, ", "))
						return fmt.Errorf("plan %d (%s): failed to read: %w", i+1, filePath, err)
					}
					plan, err := converters.ToDomainPlan(data)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Plan files retained for debugging: %s\n", strings.Join(files, ", "))
						return fmt.Errorf("plan %d (%s): %w", i+1, filePath, err)
					}
					result, err := surgeon.ExecutePlan(ctx, plan)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Plan files retained for debugging: %s\n", strings.Join(files, ", "))
						return fmt.Errorf("plan %d (%s): %w", i+1, filePath, err)
					}
					for _, w := range result.Warnings {
						fmt.Printf("WARNING: %s\n", w)
					}
					totalFilesModified += result.FilesModified
				}
				if !keep {
					for _, filePath := range files {
						os.Remove(filePath)
					}
					fmt.Printf("SUCCESS: %d files modified (%d plans). Cleaned up %d plan files.\n", totalFilesModified, len(files), len(files))
				} else {
					fmt.Printf("SUCCESS: %d files modified (%d plans).\n", totalFilesModified, len(files))
				}
				return nil
			}

			// Single-file / stdin mode (backward compatibility, no auto-cleanup)
			var input io.Reader
			if len(args) > 0 && args[0] != "" {
				f, err := os.Open(args[0])
				if err != nil {
					return fmt.Errorf("failed to open file: %w", err)
				}
				defer f.Close()
				input = f
			} else {
				input = os.Stdin
			}

			data, err := io.ReadAll(input)
			if err != nil {
				return fmt.Errorf("failed to read input: %w", err)
			}

			plan, err := converters.ToDomainPlan(data)
			if err != nil {
				return err
			}

			result, err := surgeon.ExecutePlan(ctx, plan)
			if err != nil {
				return err
			}

			for _, w := range result.Warnings {
				fmt.Printf("WARNING: %s\n", w)
			}
			fmt.Printf("SUCCESS: %d files modified.\n", result.FilesModified)
			return nil
		},
	}

	cmd.Flags().StringArrayVarP(&files, "file", "f", nil, "plan YAML file to execute (repeatable; auto-cleanup on success)")
	cmd.Flags().BoolVarP(&keep, "keep", "k", false, "retain plan files even on success (only applies with --file/-f)")
	return cmd
}
