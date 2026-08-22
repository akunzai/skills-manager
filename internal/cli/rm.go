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
		flagAgents []string
		flagYes    bool
	)

	cmd := &cobra.Command{
		Use:     "rm [skills...]",
		Aliases: []string{"remove"},
		Short:   "Remove one or more skills globally or from specific agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Past flag parsing, every failure below is a runtime problem rather
			// than misuse, so reporting it with a usage dump would mislead.
			cmd.SilenceUsage = true
			configPath, skillsDir, _ := GetEffectivePaths()

			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}

			skillsToRemove := args

			if len(skillsToRemove) == 0 {
				if tui.IsTerminal() && !flagYes {
					allSkills := engine.ScanAllSkills(cfg, skillsDir)
					if len(allSkills) == 0 {
						fmt.Printf("%sNo skills installed or configured to remove.%s\n", colorYellow, colorReset)
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
						fmt.Printf("%sOperation cancelled.%s\n", colorYellow, colorReset)
						return nil
					}
					if len(chosen) == 0 {
						fmt.Printf("%sNo skills selected. Aborted.%s\n", colorYellow, colorReset)
						return nil
					}
					skillsToRemove = chosen
				} else {
					cmd.SilenceUsage = false
					return fmt.Errorf("skill name(s) required")
				}
			}

			for _, skillName := range skillsToRemove {
				fmt.Printf("\n%s🗑  Removing skill: %s%s%s...\n", colorCyan, colorBold, skillName, colorReset)

				// 1. Remove only from specific agent(s)
				if len(flagAgents) > 0 {
					knownAgents := models.GetAgentsForSkillsDir(skillsDir)
					for _, agent := range flagAgents {
						norm := models.NormalizeAgentName(agent)
						if agentDir, ok := knownAgents[norm]; ok {
							linkPath := filepath.Join(agentDir, skillName)
							_ = os.RemoveAll(linkPath)
							fmt.Printf("  %s✔%s Removed link from %s\n", colorGreen, colorReset, norm)
						}
					}
					continue
				}

				// 2. Full removal
				unlinked := engine.RemoveAgentSymlinks(skillName, skillsDir)
				if len(unlinked) > 0 {
					fmt.Printf("  %s✔%s Unlinked from: %s\n", colorGreen, colorReset, strings.Join(unlinked, ", "))
				}

				masterPath := filepath.Join(skillsDir, skillName)
				if _, err := os.Lstat(masterPath); err == nil {
					_ = os.RemoveAll(masterPath)
					fmt.Printf("  %s✔%s Removed master directory: %s\n", colorGreen, colorReset, models.ToTildePath(masterPath))
				}

				if config.RemoveSkillEntry(cfg, skillName) {
					fmt.Printf("  %s✔%s Removed from configuration\n", colorGreen, colorReset)
				}
			}

			if err := config.SaveConfig(cfg, configPath); err != nil {
				return err
			}

			fmt.Printf("\n%s✨ Skill removal complete!%s\n\n", colorGreen, colorReset)
			return nil
		},
	}

	cmd.Flags().StringSliceVarP(&flagAgents, "agent", "a", nil, "Remove only from specific agents")
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip confirmation prompts")

	return cmd
}
