package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
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

There is no limit on the number of actions per plan file.`,
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
						return fmt.Errorf("plan %d (%s): %w [hint: check YAML indentation, use '|' for multi-line content blocks indented 2+ spaces from 'content:']", i+1, filePath, err)
					}
					result, err := surgeon.ExecutePlan(ctx, plan)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Plan files retained for debugging: %s\n", strings.Join(files, ", "))
						hint := executeErrorHint(err)
						if hint != "" {
							return fmt.Errorf("plan %d (%s): %w\n%s", i+1, filePath, err, hint)
						}
						return fmt.Errorf("plan %d (%s): %w", i+1, filePath, err)
					}
					for _, w := range result.Warnings {
						fmt.Printf("WARNING: %s\n", w)
					}
					totalFilesModified += result.FilesModified
				}
				if !keep {
					for _, filePath := range files {
						_ = os.Remove(filePath)
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
				defer func() { _ = f.Close() }()
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
				return fmt.Errorf("%w [hint: check YAML indentation, use '|' for multi-line content blocks indented 2+ spaces from 'content:']", err)
			}

			result, err := surgeon.ExecutePlan(ctx, plan)
			if err != nil {
				hint := executeErrorHint(err)
				if hint != "" {
					return fmt.Errorf("%w\n%s", err, hint)
				}
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

// executeErrorHint returns an actionable hint for known plan execution errors.
func executeErrorHint(err error) string {
	var de *domain.Error
	if !errors.As(err, &de) {
		return ""
	}
	switch de.Code {
	case "NODE_ALREADY_EXISTS":
		return "Hint: use 'update_func' or 'update_struct' instead of 'add_func'/'add_struct' to replace an existing declaration."
	case "NODE_NOT_FOUND":
		return "Hint: use 'go-surgeon symbol <identifier>' to verify it exists, or check the 'identifier:' field in your plan."
	case "FILE_NOT_FOUND":
		return "Hint: check the 'file:' path in your plan. Use 'go-surgeon graph' to list available packages."
	case "PARSE_ERROR":
		return "Hint: check that 'content:' is valid Go source. Omit the 'package' declaration and imports — goimports handles them."
	}
	return ""
}
