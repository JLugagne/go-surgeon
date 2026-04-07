package commands

import (
	"context"
	"flag"
	"fmt"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
)

// ImplementCommand handles the CLI implementation of an interface.
type ImplementCommand struct {
	surgeon service.SurgeonCommands
}

// NewImplementCommand creates a new ImplementCommand.
func NewImplementCommand(surgeon service.SurgeonCommands) *ImplementCommand {
	return &ImplementCommand{surgeon: surgeon}
}

// Run executes the implement command.
func (c *ImplementCommand) Run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("implement", flag.ContinueOnError)
	recv := fs.String("receiver", "", "Receiver type (e.g. '*MyStruct')")
	file := fs.String("file", "", "Target file to append to")

	var posArgs []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return err
		}
		if fs.NArg() > 0 {
			posArgs = append(posArgs, fs.Arg(0))
			args = fs.Args()[1:]
		} else {
			break
		}
	}
	if len(posArgs) < 1 {
		return fmt.Errorf("usage: implement <package.Interface> --receiver <Receiver> --file <File>")
	}

	if *recv == "" || *file == "" {
		return fmt.Errorf("--receiver and --file flags are required")
	}

	ifaceStr := posArgs[0]
	
	req := domain.ImplementRequest{
		Interface: ifaceStr,
		Receiver:  *recv,
		FilePath:  *file,
	}

	results, err := c.surgeon.Implement(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to implement interface: %w", err)
	}

	if len(results) == 0 {
		fmt.Println("All methods are already implemented!")
		return nil
	}

	fmt.Printf("Generated %d missing methods for %s:\n\n", len(results), ifaceStr)
	for _, res := range results {
		bodyLines := res.LineEnd - res.LineStart + 1
		fmt.Printf("Symbol: %s\n", res.Name)
		fmt.Printf("Receiver: %s\n", res.Receiver)
		fmt.Printf("File: %s:%d-%d (%d lines body)\n", res.File, res.LineStart, res.LineEnd, bodyLines)
		fmt.Printf("Code (Empty lines stripped):\n%s\n\n", res.Code)
	}

	return nil
}
