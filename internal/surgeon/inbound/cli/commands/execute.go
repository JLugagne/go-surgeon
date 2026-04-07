package commands

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/JLugagne/go-surgeon/internal/surgeon/inbound/converters"
)

// ExecutePlanCommand handles the CLI execution of a surgery plan.
type ExecutePlanCommand struct {
	surgeon service.SurgeonCommands
}

// NewExecutePlanCommand creates a new ExecutePlanCommand.
func NewExecutePlanCommand(surgeon service.SurgeonCommands) *ExecutePlanCommand {
	return &ExecutePlanCommand{surgeon: surgeon}
}

// Run executes the command logic.
func (c *ExecutePlanCommand) Run(ctx context.Context, args []string) error {
	var input io.Reader
	var err error

	// If an argument is provided, treat it as a file path.
	// Otherwise, read from stdin.
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

	count, err := c.surgeon.ExecutePlan(ctx, plan)
	if err != nil {
		return err
	}

	fmt.Printf("SUCCESS: %d files modified.\n", count)
	return nil
}
