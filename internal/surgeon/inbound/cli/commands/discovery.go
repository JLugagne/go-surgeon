package commands

import (
	"fmt"

	"github.com/JLugagne/go-surgeon/internal/surgeon/discovery"
	"github.com/spf13/cobra"
)

func NewDiscoveryCommand() *cobra.Command {
	var category string
	cmd := &cobra.Command{
		Use:   "discovery [tool|tool.op]",
		Short: "Print the go-surgeon tool catalog (no args) or per-tool/per-op detail",
		Long: `discovery prints documentation for go-surgeon's tools, the same content the
MCP server used to expose via describe_tool. Run it from a shell to keep the
MCP tool descriptions short and reduce context bloat in agent sessions.

Examples:
  go-surgeon discovery                  # grouped list of every tool
  go-surgeon discovery patch            # detail for one tool
  go-surgeon discovery patch.function   # detail for one op of a tool
  go-surgeon discovery --category edit  # filter the list to one category`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			out, err := discovery.Render(name, category)
			if err != nil {
				return err
			}
			fmt.Print(out)
			return nil
		},
	}
	cmd.Flags().StringVar(&category, "category", "", "filter the list to one category (explore, refs, edit, interface, codegen, validate, batch)")
	return cmd
}
