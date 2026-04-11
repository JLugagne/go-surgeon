package commands

import (
	"fmt"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/spf13/cobra"
)

// NewInsertCallCommand creates the insert-call subcommand.
func NewInsertCallCommand(surgeon service.SurgeonCommands) *cobra.Command {
	var file string
	var id string
	var call string
	var position string

	cmd := &cobra.Command{
		Use:   "insert-call",
		Short: "Insert a statement into a function body at a specific position",
		Long: `Inserts a single statement into the body of the named function.

--id accepts "FuncName" for free functions or "Receiver.Method" for methods.
--call is the raw statement to insert (without trailing semicolon).
--position controls where the statement is placed:

  before-return   insert before the last top-level return statement (default)
  end-of-body     insert at the end of the function body before the closing brace
  after:<marker>  insert after the first line that contains <marker>
                  e.g. --position "after:// routes"

The operation is idempotent: if the exact call is already present in the
function body, it is skipped with a warning.`,
		Example: `  # Insert before the return statement
  go-surgeon insert-call \
    --file internal/catalog/wire_order.go \
    --id wireOrder \
    --call "setupPayOrderRoute(mux, app)"

  # Insert at the end of the body
  go-surgeon insert-call \
    --file internal/catalog/wire_order.go \
    --id wireOrder \
    --call "setupCancelOrderRoute(mux, app)" \
    --position end-of-body

  # Insert after a marker comment
  go-surgeon insert-call \
    --file internal/catalog/wire_order.go \
    --id wireOrder \
    --call "setupFulfillOrderRoute(mux, app)" \
    --position "after:// order routes"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			result, err := surgeon.ExecutePlan(ctx, domain.Plan{Actions: []domain.Action{{
				Action:     domain.ActionTypeInsertCall,
				FilePath:   file,
				Identifier: id,
				Content:    call,
				Position:   domain.InsertPosition(position),
			}}})
			if err != nil {
				return fmt.Errorf("ERROR (insert-call): %w", err)
			}

			for _, w := range result.Warnings {
				fmt.Printf("WARNING (insert-call): %s\n", w)
			}
			fmt.Printf("SUCCESS (insert-call): %s in %s\n", id, file)
			return nil
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "Target file path (required)")
	_ = cmd.MarkFlagRequired("file")
	cmd.Flags().StringVarP(&id, "id", "i", "", "Function identifier, e.g. FuncName or Receiver.Method (required)")
	_ = cmd.MarkFlagRequired("id")
	cmd.Flags().StringVar(&call, "call", "", "Statement to insert, e.g. setupPayOrderRoute(mux, app) (required)")
	_ = cmd.MarkFlagRequired("call")
	cmd.Flags().StringVar(&position, "position", "before-return", `Insertion position: before-return, end-of-body, or after:<marker>`)

	return cmd
}
