package commands

import (
	"fmt"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/spf13/cobra"
)

func NewTagCommand(surgeon service.SurgeonCommands) *cobra.Command {
	var req domain.TagRequest

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

			fmt.Printf("SUCCESS (tag): Updated tags for %s in %s\n", req.StructName, req.FilePath)
			return nil
		},
	}

	cmd.Flags().StringVarP(&req.FilePath, "file", "f", "", "Target Go file containing the struct")
	cmd.Flags().StringVarP(&req.StructName, "id", "i", "", "Struct identifier")
	cmd.Flags().StringVar(&req.FieldName, "field", "", "Specific field name to update")
	cmd.Flags().StringVar(&req.SetTag, "set", "", "Exact tag string to set/append")
	cmd.Flags().StringVar(&req.AutoFormat, "auto", "", "Auto-generate tags for exported fields (e.g. json, bson)")

	cmd.MarkFlagRequired("file")
	cmd.MarkFlagRequired("id")

	return cmd
}
