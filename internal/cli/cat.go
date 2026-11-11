package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/spf13/cobra"
)

func newCatCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cat <skill>",
		Short: "Print the SKILL.md an Agent reads for a skill",
		Long: `Print a skill's SKILL.md exactly as it is on the Scope skills directory, the
copy Agents read, so it can be redirected or piped into an Agent. Any skill
'skills ls' lists there can be printed, declared or Untracked.

Exits 0 when the SKILL.md is printed, 1 when the skill is not readable on
disk (nothing is printed; the next action goes to stderr), and 2 when no
skill has that name in the Scope.`,
		Example: "  skills cat my-skill\n  skills cat my-skill -p | claude",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Past argument parsing, every failure is a runtime problem.
			cmd.SilenceUsage = true
			name := args[0]
			scope := ResolveScope(cmd)
			cfg, err := config.LoadConfig(scope.ConfigPath)
			if err != nil {
				return err
			}
			inv, err := engine.LoadInventory(cfg, scope)
			if err != nil {
				return err
			}
			item, found := findSkillItem(inv.SkillItems(nil), name)
			if !found {
				return fmt.Errorf("no Skill %s in this Scope", name)
			}
			flags := scopeFlagsOf(cmd, scope)
			unreadable := func(state, next string) error {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s %s.\nNext: run 'skills %s%s'.\n", name, state, next, flags)
				return exitError{message: fmt.Sprintf("%s %s", name, state), code: 1}
			}
			switch item.Status {
			case models.SkillStatusPresent, models.SkillStatusUntracked, models.SkillStatusUntrackedLink:
			case models.SkillStatusMissing:
				return unreadable("is declared but not on the skills directory", "sync")
			default:
				return unreadable(fmt.Sprintf("is %s on the skills directory", item.Status), "doctor")
			}
			// An Untracked file, or a directory without one, has no SKILL.md.
			if info, err := os.Stat(item.InstalledPath); err != nil || !info.IsDir() {
				return unreadable("has no SKILL.md", "doctor")
			}
			body, err := os.ReadFile(filepath.Join(item.InstalledPath, "SKILL.md"))
			if errors.Is(err, fs.ErrNotExist) {
				return unreadable("has no SKILL.md", "doctor")
			}
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(body)
			return err
		},
	}
}

func findSkillItem(items []models.SkillItem, name string) (models.SkillItem, bool) {
	for _, item := range items {
		if item.Name == name {
			return item, true
		}
	}
	return models.SkillItem{}, false
}
