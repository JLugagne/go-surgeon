package commands

import (
	"fmt"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/spf13/cobra"
)

func NewImplementCommand(surgeon service.SurgeonCommands) *cobra.Command {
	var receiver string
	var file string

	cmd := &cobra.Command{
		Use:   "implement <package.Interface>",
		Short: "Generate missing interface method stubs on a struct",
		Long: `Resolves the target interface via go/packages (supports stdlib, third-party, and
project-local interfaces) and appends method stubs to --file.

Existing methods that already satisfy the interface are skipped. Each generated stub
contains a // TODO: implement comment and panics with "not implemented". Fill in the
body of each stub with update-func.

For interfaces you own within this project, prefer add-interface which creates both
the interface file and its mock in one step.`,
		Example: `  # Implement a stdlib interface
  go-surgeon implement io.ReadCloser --receiver "*MyReader" --file internal/pkg/reader.go

  # Implement a third-party interface
  go-surgeon implement context.Context --receiver "*MyCtx" --file internal/ctx.go

  # Short flags  (-r --receiver, -f --file)
  go-surgeon implement io.Writer -r "*MyWriter" -f internal/pkg/writer.go`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			req := domain.ImplementRequest{
				Interface: args[0],
				Receiver:  receiver,
				FilePath:  file,
			}

			results, err := surgeon.Implement(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to implement interface: %w", err)
			}

			if len(results) == 0 {
				fmt.Println("All methods are already implemented!")
				return nil
			}

			fmt.Printf("Generated %d missing methods for %s:\n\n", len(results), args[0])
			for _, res := range results {
				bodyLines := res.LineEnd - res.LineStart + 1
				fmt.Printf("Symbol: %s\n", res.Name)
				fmt.Printf("Receiver: %s\n", res.Receiver)
				fmt.Printf("File: %s:%d-%d (%d lines body)\n", res.File, res.LineStart, res.LineEnd, bodyLines)
				fmt.Printf("Code (Empty lines stripped):\n%s\n\n", res.Code)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&receiver, "receiver", "r", "", "Receiver type, e.g. '*MyStruct' (required)")
	_ = cmd.MarkFlagRequired("receiver")
	cmd.Flags().StringVarP(&file, "file", "f", "", "Target file to append stubs to (required)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}
