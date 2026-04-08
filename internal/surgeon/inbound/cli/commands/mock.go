package commands

import (
	"fmt"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/spf13/cobra"
)

func NewMockCommand(surgeon service.SurgeonCommands) *cobra.Command {
	var mockName string
	var file string

	cmd := &cobra.Command{
		Use:   "mock <package.Interface>",
		Short: "Generate a function-field mock for an interface",
		Long: `Generates a function-field mock for any interface — stdlib, third-party, or
project-local. The generated mock struct has a FuncField for every method, delegation
methods that call the field, and a compile-time interface check.

Calling a method whose FuncField is nil panics with a clear "not set" message,
making missing test setup immediately visible.

For interfaces you own within this project, prefer add-interface which creates both
the interface and its mock in one step.`,
		Example: `  # Mock a stdlib interface
  go-surgeon mock io.ReadCloser --mock-name MockReadCloser --file internal/mocks/readcloser.go

  # Mock a project-local interface (full import path)
  go-surgeon mock github.com/myorg/myapp/domain.Repository \
    --mock-name MockRepository \
    --file internal/domain/repositorytest/mock.go

  # Short flags  (-m --mock-name, -f --file)
  go-surgeon mock io.Writer -m MockWriter -f internal/mocks/writer.go`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			req := domain.MockRequest{
				Interface: args[0],
				Receiver:  mockName,
				FilePath:  file,
			}

			result, err := surgeon.Mock(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to generate mock: %w", err)
			}
			fmt.Printf("SUCCESS: %s\n", result)
			return nil
		},
	}
	cmd.Flags().StringVarP(&mockName, "mock-name", "m", "", "Mock struct name, e.g. 'MockBookRepository' (required)")
	_ = cmd.MarkFlagRequired("mock-name")
	cmd.Flags().StringVarP(&file, "file", "f", "", "Target file to write the mock to (required)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}
