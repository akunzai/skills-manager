package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/akunzai/skills-manager/internal/tui"
	"github.com/spf13/cobra"
)

func newRmCmd() *cobra.Command {
	var (
		flagYes bool
	)

	cmd := &cobra.Command{
		Use:     "rm [skills...]",
		Aliases: []string{"remove"},
		Short:   "Remove one or more skills",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Past flag parsing, every failure below is a runtime problem rather
			// than misuse, so reporting it with a usage dump would mislead.
			cmd.SilenceUsage = true
			out := cmd.OutOrStdout()
			configPath, skillsDir, _ := GetEffectivePaths()

			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}

			skillsToRemove := args

			if len(skillsToRemove) == 0 {
				if tui.IsTerminal() && !flagYes {
					allSkills, err := engine.Inventory(cfg, skillsDir)
					if err != nil {
						return err
					}
					if len(allSkills) == 0 {
						fmt.Fprintf(out, "%sNo skills installed or configured to remove.%s\n", colorYellow, colorReset)
						return nil
					}

					groups := make(map[string][]tui.SelectOption)
					for _, s := range allSkills {
						srcKey := s.Source
						groups[srcKey] = append(groups[srcKey], tui.SelectOption{
							Key:       s.Name,
							Title:     s.Name,
							Installed: s.IsInstalled,
							Selected:  false,
						})
					}

					chosen, err := tui.PromptGroupedMultiSelect("Select skills to remove:", groups)
					if err != nil {
						return err
					}
					if chosen == nil {
						fmt.Fprintf(out, "%sOperation cancelled.%s\n", colorYellow, colorReset)
						return nil
					}
					if len(chosen) == 0 {
						fmt.Fprintf(out, "%sNo skills selected. Aborted.%s\n", colorYellow, colorReset)
						return nil
					}
					skillsToRemove = chosen
				} else {
					cmd.SilenceUsage = false
					return fmt.Errorf("skill name(s) required")
				}
			}

			for _, skillName := range skillsToRemove {
				fmt.Fprintf(out, "\n%sRemoving skill: %s%s%s...\n", colorCyan, colorBold, skillName, colorReset)

				// Full removal
				unlinked := engine.RemoveAgentSymlinks(skillName, skillsDir)
				if len(unlinked) > 0 {
					fmt.Fprintf(out, "  %sUnlinked from: %s.%s\n", colorGreen, strings.Join(unlinked, ", "), colorReset)
				}

				masterPath := filepath.Join(skillsDir, skillName)
				if _, err := os.Lstat(masterPath); err == nil {
					_ = os.RemoveAll(masterPath)
					fmt.Fprintf(out, "  %sRemoved master directory: %s.%s\n", colorGreen, models.ToTildePath(masterPath), colorReset)
				}

				if config.RemoveSkillEntry(cfg, skillName) {
					fmt.Fprintf(out, "  %sRemoved from configuration.%s\n", colorGreen, colorReset)
				}
			}

			if err := config.SaveConfig(cfg, configPath); err != nil {
				return err
			}

			fmt.Fprintf(out, "\n%sSkill removal complete.%s\n\n", colorGreen, colorReset)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip confirmation prompts")

	return cmd
}
