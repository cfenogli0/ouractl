package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cfenogli0/ouractl/internal/skill"
	"github.com/spf13/cobra"
)

// defaultSkillDir is the personal Claude Code skills location for octl.
func defaultSkillDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "skills", "octl"), nil
}

func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage the octl Claude Code skill",
	}
	cmd.AddCommand(newSkillInstallCmd())
	return cmd
}

func newSkillInstallCmd() *cobra.Command {
	var dir string
	var force bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the bundled Claude Code skill into your skills directory",
		RunE: func(cmd *cobra.Command, _ []string) error {
			target := dir
			if target == "" {
				d, err := defaultSkillDir()
				if err != nil {
					return err
				}
				target = d
			}
			path, err := skill.Install(target, force)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "installed octl skill to %s\nrestart Claude Code (or start a new session) to load it\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "target skills directory (default ~/.claude/skills/octl)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing SKILL.md")
	return cmd
}
