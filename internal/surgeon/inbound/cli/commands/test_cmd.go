package commands

import (
	"fmt"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/spf13/cobra"
)

func NewTestCommand(surgeon service.SurgeonCommands) *cobra.Command {
	var filePath string
	var identifier string

	cmd := &cobra.Command{
		Use:   "test",
		Short: "Generate table-driven test skeleton for a function or method",
		RunE: func(cmd *cobra.Command, args []string) error {
			if filePath == "" || identifier == "" {
				return fmt.Errorf("both --file and --id are required")
			}

			// Generate test
			testFile, err := surgeon.GenerateTest(cmd.Context(), filePath, identifier)
			if err != nil {
				return err
			}

			fmt.Printf("SUCCESS (test): Generated test skeleton in %s\n", testFile)
			return nil
		},
	}

	cmd.Flags().StringVarP(&filePath, "file", "f", "", "Target Go file containing the function")
	cmd.Flags().StringVarP(&identifier, "id", "i", "", "Function or method identifier (e.g. NewApp, (*App).Start)")
	_ = cmd.MarkFlagRequired("file")
	_ = cmd.MarkFlagRequired("id")

	return cmd
}
