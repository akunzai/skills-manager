package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	var flagFix bool

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose and repair skills health",
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, skillsDir, _ := GetEffectivePaths()

			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}

			fmt.Printf("\n%s%s🩺 Diagnosing Skills Health...%s\n\n", colorBold, colorCyan, colorReset)
			issuesFound := 0

			// 1. Check master skills dir
			if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
				fmt.Printf("%s✖ Master skills directory does not exist: %s%s\n", colorRed, skillsDir, colorReset)
				issuesFound++
			} else {
				fmt.Printf("%s✔%s Master skills directory: %s\n", colorGreen, colorReset, skillsDir)
			}

			// 2. Check broken symlinks in agent directories
			fmt.Printf("\n%sChecking Agent Directories & Symlinks:%s\n", colorBold, colorReset)
			knownAgents := models.GetAgentsForSkillsDir(skillsDir)

			for agentName, agentDir := range knownAgents {
				if _, err := os.Stat(agentDir); os.IsNotExist(err) {
					continue
				}

				var brokenInAgent []string
				var physicalInAgent []string

				entries, err := os.ReadDir(agentDir)
				if err != nil {
					continue
				}

				for _, entry := range entries {
					name := entry.Name()
					fullP := filepath.Join(agentDir, name)

					fi, err := os.Lstat(fullP)
					if err != nil {
						continue
					}

					if fi.Mode()&os.ModeSymlink != 0 {
						if _, err := os.Stat(fullP); err != nil {
							brokenInAgent = append(brokenInAgent, name)
						}
					} else if fi.IsDir() && !strings.HasPrefix(name, ".") {
						physicalInAgent = append(physicalInAgent, name)
					}
				}

				if len(brokenInAgent) > 0 {
					fmt.Printf("  %s✖ [%s] Broken symlinks:%s %s\n", colorRed, agentName, colorReset, strings.Join(brokenInAgent, ", "))
					issuesFound += len(brokenInAgent)
					if flagFix {
						for _, b := range brokenInAgent {
							_ = os.Remove(filepath.Join(agentDir, b))
							fmt.Printf("    %s✔ Fixed: Removed broken symlink %s%s\n", colorGreen, b, colorReset)
						}
					}
				} else {
					fmt.Printf("  %s✔%s [%s] Symlinks healthy (%s)\n", colorGreen, colorReset, agentName, agentDir)
				}

				if len(physicalInAgent) > 0 {
					fmt.Printf("  %s⚠️  [%s] Physical directories found instead of symlinks:%s %s\n", colorYellow, agentName, colorReset, strings.Join(physicalInAgent, ", "))
					issuesFound += len(physicalInAgent)
					if flagFix {
						for _, pName := range physicalInAgent {
							masterSkillPath := filepath.Join(skillsDir, pName)
							if _, err := os.Stat(masterSkillPath); err == nil {
								_ = os.RemoveAll(filepath.Join(agentDir, pName))
								_, _ = engine.EnsureAgentSymlink(pName, agentName, skillsDir)
								fmt.Printf("    %s✔ Fixed: Converted %s in %s to symlink%s\n", colorGreen, pName, agentName, colorReset)
							}
						}
					}
				}
			}

			// 3. Check configured vs installed
			skills := engine.ScanAllSkills(cfg, skillsDir)
			var missing []string
			var untracked []string
			var invalid []string

			for _, s := range skills {
				if !s.IsInstalled {
					missing = append(missing, s.Name)
				} else if s.SourceType == "untracked" {
					untracked = append(untracked, s.Name)
				} else if !s.IsValidSkill {
					invalid = append(invalid, s.Name)
				}
			}

			if len(missing) > 0 {
				fmt.Printf("\n%s⚠️  Configured but missing skills:%s %s\n", colorYellow, colorReset, strings.Join(missing, ", "))
				issuesFound += len(missing)
			}
			if len(untracked) > 0 {
				fmt.Printf("\n%s⚠️  Untracked skills in %s:%s %s\n", colorYellow, skillsDir, colorReset, strings.Join(untracked, ", "))
			}
			if len(invalid) > 0 {
				fmt.Printf("\n%s✖ Installed folders missing SKILL.md:%s %s\n", colorRed, colorReset, strings.Join(invalid, ", "))
				issuesFound += len(invalid)
			}

			fmt.Println("\n" + strings.Repeat("─", 60))
			if issuesFound == 0 {
				fmt.Printf("%s%s🎉 Everything is in top condition! No issues detected.%s\n\n", colorBold, colorGreen, colorReset)
				return nil
			}

			fmt.Printf("%s%sFound %d issue(s). Run with --fix or 'skills sync' to repair.%s\n\n", colorBold, colorYellow, issuesFound, colorReset)
			return fmt.Errorf("doctor detected %d issue(s)", issuesFound)
		},
	}

	cmd.Flags().BoolVar(&flagFix, "fix", false, "Automatically repair detected issues")

	return cmd
}
