package commands

import (
	"fmt"

	"github.com/JLugagne/go-surgeon/internal/surgeon/domain"
	"github.com/JLugagne/go-surgeon/internal/surgeon/domain/service"
	"github.com/spf13/cobra"
)

// NewReadCommand wires the read primitive: whole-file or line-range reads
// of Go sources with the same numbering as `symbol --body`.
func NewReadCommand(queries service.SurgeonQueries) *cobra.Command {
	var from, to, maxBytes int

	cmd := &cobra.Command{
		Use:   "read <file>",
		Short: "Read a Go file (or line range) with line numbers",
		Long: `Reads a Go source file and prints its content with line numbers, using the
same numbering as 'symbol --body'. Use --from/--to to read a range and
--max-bytes to cap large outputs (a truncation notice is printed).

'read file <path>' is accepted as an alias for 'read <path>'.`,
		Example: `  go-surgeon read internal/surgeon/init.go
  go-surgeon read internal/surgeon/init.go --from 20 --to 60
  go-surgeon read file internal/surgeon/init.go --max-bytes 4096`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file := args[0]
			if file == "file" && len(args) > 1 {
				file = args[1]
			}
			res, err := queries.ReadFile(cmd.Context(), domain.ReadFileRequest{
				FilePath: file,
				FromLine: from,
				ToLine:   to,
				MaxBytes: maxBytes,
			})
			if err != nil {
				return err
			}
			fmt.Println(res.Content)
			if res.Notice != "" {
				fmt.Printf("\n%s\n", res.Notice)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&from, "from", 0, "First line to read (1-based, inclusive)")
	cmd.Flags().IntVar(&to, "to", 0, "Last line to read (1-based, inclusive)")
	cmd.Flags().IntVar(&maxBytes, "max-bytes", 0, "Cap the output in bytes (default 262144)")
	return cmd
}
