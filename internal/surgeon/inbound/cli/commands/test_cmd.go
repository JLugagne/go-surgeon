package commands

import (
	"fmt"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/spf13/cobra"
)

func NewTestCommand(surgeon service.SurgeonCommands) *cobra.Command {
	var filePath string
	var identifier string
	var silent bool

	cmd := &cobra.Command{
		Use:   "test",
		Short: "Generate table-driven test skeleton for a function or method",
		RunE: func(cmd *cobra.Command, args []string) error {
			if filePath == "" || identifier == "" {
				return fmt.Errorf("both --file and --id are required")
			}

			testFile, err := surgeon.GenerateTest(cmd.Context(), filePath, identifier)
			if err != nil {
				return err
			}

			if silent {
				return nil
			}

			fmt.Printf("SUCCESS (test): Generated test skeleton in %s\n", testFile)
			symbols := parseFileSymbols(testFile)
			if len(symbols) > 0 {
				printSymbols("test functions", symbols)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&filePath, "file", "f", "", "Target Go file containing the function")
	cmd.Flags().StringVarP(&identifier, "id", "i", "", "Function or method identifier (e.g. NewApp, (*App).Start)")
	cmd.Flags().BoolVar(&silent, "silent", false, "Suppress output (errors are still reported)")
	_ = cmd.MarkFlagRequired("file")
	_ = cmd.MarkFlagRequired("id")

	return cmd
}
