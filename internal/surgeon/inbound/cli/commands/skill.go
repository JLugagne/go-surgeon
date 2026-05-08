package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/JLugagne/go-surgeon/internal/surgeon/discovery"
	"github.com/spf13/cobra"
)

func NewSkillCommand() *cobra.Command {
	var outDir string
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Print a Claude SKILL.md describing how to use go-surgeon",
		Long: `skill emits a Claude-compatible SKILL.md for go-surgeon. Pipe it where you
need it, or pass --out <dir> to write SKILL.md directly into that directory
(typically .claude/skills/go-surgeon/).

Examples:
  go-surgeon skill                                    # SKILL.md to stdout
  go-surgeon skill --out .claude/skills/go-surgeon/   # write SKILL.md to that dir`,
		RunE: func(cmd *cobra.Command, args []string) error {
			content := discovery.RenderSkill()
			if outDir == "" {
				fmt.Print(content)
				return nil
			}
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return fmt.Errorf("create skill dir: %w", err)
			}
			path := filepath.Join(outDir, "SKILL.md")
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&outDir, "out", "", "write SKILL.md into this directory instead of stdout")
	return cmd
}
