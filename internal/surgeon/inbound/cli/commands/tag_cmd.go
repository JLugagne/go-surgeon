package commands

import (
	"fmt"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/spf13/cobra"
)

func NewTagCommand(surgeon service.SurgeonCommands) *cobra.Command {
	var req domain.TagRequest
	var silent bool

	cmd := &cobra.Command{
		Use:   "tag",
		Short: "Manipulate struct tags",
		RunE: func(cmd *cobra.Command, args []string) error {
			if req.FilePath == "" || req.StructName == "" {
				return fmt.Errorf("both --file and --id are required")
			}
			if req.SetTag == "" && req.AutoFormat == "" {
				return fmt.Errorf("either --set or --auto must be provided")
			}

			if err := surgeon.TagStruct(cmd.Context(), req); err != nil {
				return err
			}

			if silent {
				return nil
			}

			fmt.Printf("SUCCESS (tag): Updated tags for %s in %s\n", req.StructName, req.FilePath)
			symbols := parseFileSymbols(req.FilePath)
			for _, s := range symbols {
				if s.Name == req.StructName {
					fmt.Printf("\n%s:%d-%d\n  %s\n", s.File, s.LineStart, s.LineEnd, s.Name)
					break
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&req.FilePath, "file", "f", "", "Target Go file containing the struct")
	cmd.Flags().StringVarP(&req.StructName, "id", "i", "", "Struct identifier")
	cmd.Flags().StringVar(&req.FieldName, "field", "", "Specific field name to update")
	cmd.Flags().StringVar(&req.SetTag, "set", "", "Exact tag string to set/append")
	cmd.Flags().StringVar(&req.AutoFormat, "auto", "", "Auto-generate tags for exported fields (e.g. json, bson)")
	cmd.Flags().BoolVar(&silent, "silent", false, "Suppress output (errors are still reported)")

	_ = cmd.MarkFlagRequired("file")
	_ = cmd.MarkFlagRequired("id")

	return cmd
}
