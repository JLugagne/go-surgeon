package commands

import (
	"fmt"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/spf13/cobra"
)

func NewExtractInterfaceCommand(surgeon service.SurgeonCommands) *cobra.Command {
	var req domain.ExtractInterfaceRequest
	var silent bool

	cmd := &cobra.Command{
		Use:   "extract-interface",
		Short: "Extract interface from a struct",
		RunE: func(cmd *cobra.Command, args []string) error {
			if req.FilePath == "" || req.StructName == "" || req.InterfaceName == "" {
				return fmt.Errorf("--file, --id, and --name are required")
			}

			interfaceFile, err := surgeon.ExtractInterface(cmd.Context(), req)
			if err != nil {
				return err
			}

			if silent {
				return nil
			}

			fmt.Printf("SUCCESS (extract-interface): Extracted interface %s into %s\n", req.InterfaceName, interfaceFile)
			symbols := parseFileSymbols(interfaceFile)
			if len(symbols) > 0 {
				printSymbols("symbols", symbols)
			}
			if req.MockFile != "" {
				fmt.Printf("Mock: %s\n", req.MockFile)
				mockSymbols := parseFileSymbols(req.MockFile)
				if len(mockSymbols) > 0 {
					printSymbols("mock symbols", mockSymbols)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&req.FilePath, "file", "f", "", "Target Go file containing the struct")
	cmd.Flags().StringVarP(&req.StructName, "id", "i", "", "Struct identifier")
	cmd.Flags().StringVarP(&req.InterfaceName, "name", "n", "", "Name of the interface to create")
	cmd.Flags().StringVarP(&req.OutPath, "out", "o", "", "Optional: output file path for the interface")
	cmd.Flags().StringVarP(&req.MockFile, "mock-file", "m", "", "Optional: generate mock file path")
	cmd.Flags().StringVar(&req.MockName, "mock-name", "", "Optional: name of the mock struct")
	cmd.Flags().BoolVar(&silent, "silent", false, "Suppress output (errors are still reported)")

	_ = cmd.MarkFlagRequired("file")
	_ = cmd.MarkFlagRequired("id")
	_ = cmd.MarkFlagRequired("name")

	return cmd
}
