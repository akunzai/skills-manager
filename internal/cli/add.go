package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/akunzai/skills-manager/internal/tui"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	var (
		flagSkills      []string
		flagAll         bool
		flagPath        string
		flagURL         string
		flagBranch      string
		flagAgents      []string
		flagSymlink     string
		flagCommand     string
		flagCheck       string
		flagDescription string
		flagYes         bool
	)

	cmd := &cobra.Command{
		Use:   "add [source] [skills...]",
		Short: "Add a new skill (remote git, local symlink, or CLI command)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Past flag parsing, every failure below is a runtime problem rather
			// than misuse, so reporting it with a usage dump would mislead.
			cmd.SilenceUsage = true
			configPath, skillsDir, cacheDir := GetEffectivePaths()

			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}

			// Positional skills arguments override or append to --skill
			if len(args) > 1 {
				flagSkills = append(flagSkills, args[1:]...)
			}

			// 1. Local Symlink Mode
			if flagSymlink != "" {
				sourcePath := models.ExpandUser(flagSymlink)
				absSourcePath, err := filepath.Abs(sourcePath)
				if err != nil {
					absSourcePath = sourcePath
				}
				if _, err := os.Stat(absSourcePath); err != nil {
					return fmt.Errorf("local source path does not exist: %s", sourcePath)
				}

				skillName := filepath.Base(absSourcePath)
				if len(flagSkills) > 0 {
					skillName = flagSkills[0]
				} else if len(args) > 0 {
					skillName = args[0]
				}

				fmt.Printf("%s🔗 Linking local skill: %s%s%s -> %s\n", colorCyan, colorBold, skillName, colorReset, absSourcePath)

				if err := os.MkdirAll(skillsDir, 0755); err != nil {
					return err
				}
				destLink := filepath.Join(skillsDir, skillName)
				if err := engine.CreateSymlink(absSourcePath, destLink, true); err != nil {
					return err
				}

				targetAgents := flagAgents
				if len(targetAgents) == 0 {
					targetAgents = engine.GetTargetAgentsForSkill(skillName, "local", cfg, skillsDir)
				}
				for _, agent := range targetAgents {
					_, _ = engine.EnsureAgentSymlink(skillName, agent, skillsDir)
				}

				config.AddLocalSymlinkEntry(cfg, skillName, absSourcePath, flagDescription)
				if err := config.SaveConfig(cfg, configPath); err != nil {
					return err
				}

				fmt.Printf("%s✔ Successfully added local skill %s and updated %s%s\n", colorGreen, skillName, filepath.Base(configPath), colorReset)
				return nil
			}

			// 2. Command Skill Mode
			if flagCommand != "" {
				if len(flagSkills) == 0 && len(args) == 0 {
					cmd.SilenceUsage = false
					return fmt.Errorf("--skill <name> or skill name argument is required when adding a command skill")
				}
				skillName := ""
				if len(flagSkills) > 0 {
					skillName = flagSkills[0]
				} else {
					skillName = args[0]
				}

				fmt.Printf("%s⚙️  Configuring command skill: %s%s%s\n", colorCyan, colorBold, skillName, colorReset)
				fmt.Printf("   Command: %s\n", flagCommand)
				fmt.Println("   Executing installer command...")

				stdout, stderr, err := engine.RunCmd(flagCommand, "")
				if err != nil {
					errMsg := stderr
					if errMsg == "" {
						errMsg = stdout
					}
					fmt.Printf("%sWarning: Install command returned error: %s%s\n", colorYellow, errMsg, colorReset)
				}

				targetAgents := flagAgents
				if len(targetAgents) == 0 {
					targetAgents = engine.GetTargetAgentsForSkill(skillName, "local", cfg, skillsDir)
				}
				for _, agent := range targetAgents {
					_, _ = engine.EnsureAgentSymlink(skillName, agent, skillsDir)
				}

				config.AddLocalCommandEntry(cfg, skillName, flagCommand, flagCheck, flagDescription)
				if err := config.SaveConfig(cfg, configPath); err != nil {
					return err
				}

				fmt.Printf("%s✔ Successfully registered command skill %s and updated %s%s\n", colorGreen, skillName, filepath.Base(configPath), colorReset)
				return nil
			}

			// 3. Remote Git Repository Mode
			if len(args) == 0 {
				cmd.SilenceUsage = false
				return fmt.Errorf("source repository or --symlink/--command required")
			}
			rawSource := args[0]

			parsed := models.ParseRepoSource(rawSource)
			sourceKey := parsed.SourceKey
			cloneURL := flagURL
			if cloneURL == "" {
				cloneURL = parsed.URL
			}
			branch := flagBranch
			if branch == "" {
				branch = parsed.Branch
			}
			subpathOverride := flagPath
			if subpathOverride == "" {
				subpathOverride = parsed.Subpath
			}
			repoType := parsed.RepoType

			fmt.Printf("%s📦 Fetching repository: %s%s%s...\n", colorCyan, colorBold, sourceKey, colorReset)

			repoDir, err := engine.EnsureGitRepo(sourceKey, cloneURL, branch, true, cacheDir)
			if err != nil {
				return fmt.Errorf("error cloning repository: %w", err)
			}

			discovered, err := engine.DiscoverSkillsInRepo(repoDir)
			if err != nil || len(discovered) == 0 {
				return fmt.Errorf("no SKILL.md found in %s", sourceKey)
			}

			skillsToInstall := make(map[string]string)

			if flagAll {
				skillsToInstall = discovered
			} else if subpathOverride != "" && len(flagSkills) == 0 {
				cleanSub := filepath.ToSlash(strings.Trim(subpathOverride, "/"))
				for k, v := range discovered {
					if filepath.ToSlash(strings.Trim(v, "/")) == cleanSub {
						skillsToInstall[k] = v
					}
				}
				if len(skillsToInstall) == 0 {
					subDir := filepath.Join(repoDir, filepath.FromSlash(subpathOverride))
					if _, err := os.Stat(filepath.Join(subDir, "SKILL.md")); err == nil {
						skillsToInstall[filepath.Base(subDir)] = subpathOverride
					} else {
						return fmt.Errorf("specified path '%s' does not contain SKILL.md", subpathOverride)
					}
				}
			} else if len(flagSkills) > 0 {
				for _, sk := range flagSkills {
					if sub, ok := discovered[sk]; ok {
						skillsToInstall[sk] = sub
					} else if subpathOverride != "" {
						skillsToInstall[sk] = subpathOverride
					} else {
						// Case-insensitive match
						matched := false
						for k, v := range discovered {
							if strings.EqualFold(k, sk) {
								skillsToInstall[k] = v
								matched = true
								break
							}
						}
						if !matched {
							discNames := make([]string, 0, len(discovered))
							for k := range discovered {
								discNames = append(discNames, k)
							}
							sort.Strings(discNames)
							fmt.Printf("%sWarning: Skill '%s' not found in discovered list (%s)%s\n", colorYellow, sk, strings.Join(discNames, ", "), colorReset)
						}
					}
				}
			} else {
				if len(discovered) == 1 {
					skillsToInstall = discovered
				} else if tui.IsTerminal() && !flagYes {
					var options []tui.SelectOption
					for skName := range discovered {
						isInst := false
						if _, err := os.Stat(filepath.Join(skillsDir, skName)); err == nil {
							isInst = true
						}
						options = append(options, tui.SelectOption{
							Key:      skName,
							Title:    skName,
							Selected: isInst,
						})
					}
					sort.Slice(options, func(i, j int) bool {
						return options[i].Key < options[j].Key
					})

					chosen, err := tui.PromptMultiSelect(
						fmt.Sprintf("Select skills to install from %s:", sourceKey),
						options,
					)
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
					for _, ch := range chosen {
						skillsToInstall[ch] = discovered[ch]
					}
				} else {
					discNames := make([]string, 0, len(discovered))
					for k := range discovered {
						discNames = append(discNames, k)
					}
					sort.Strings(discNames)
					fmt.Printf("%sRepository contains multiple skills:%s %s\n", colorYellow, colorReset, strings.Join(discNames, ", "))
					fmt.Printf("Please specify --skill <name> or --all\n")
					return fmt.Errorf("multiple skills found without selection")
				}
			}

			if len(skillsToInstall) == 0 {
				return fmt.Errorf("no matching skills to install")
			}

			if err := os.MkdirAll(skillsDir, 0755); err != nil {
				return err
			}

			var storedURL string
			if flagURL != "" || repoType != "github" || !strings.HasPrefix(cloneURL, "https://github.com/") {
				storedURL = cloneURL
			}

			var installedNames []string
			for name, subpath := range skillsToInstall {
				srcPath := filepath.Join(repoDir, filepath.FromSlash(subpath))
				targetPath := filepath.Join(skillsDir, name)

				fmt.Printf("  📥 Installing %s%s%s (from %s)...\n", colorBold, name, colorReset, subpath)
				if err := engine.CopySkillFolder(srcPath, targetPath); err != nil {
					return fmt.Errorf("failed to install skill %s: %w", name, err)
				}

				targetAgents := flagAgents
				if len(targetAgents) == 0 {
					targetAgents = engine.GetTargetAgentsForSkill(name, sourceKey, cfg, skillsDir)
				}
				for _, agent := range targetAgents {
					_, _ = engine.EnsureAgentSymlink(name, agent, skillsDir)
				}

				config.AddRemoteSkillEntry(cfg, sourceKey, name, subpath, repoType, storedURL)
				installedNames = append(installedNames, name)
			}

			if err := config.SaveConfig(cfg, configPath); err != nil {
				return err
			}

			sort.Strings(installedNames)
			fmt.Printf("\n%s✔ Installed %d skill(s) [%s] and updated %s%s\n\n", colorGreen, len(installedNames), strings.Join(installedNames, ", "), filepath.Base(configPath), colorReset)
			return nil
		},
	}

	cmd.Flags().StringSliceVarP(&flagSkills, "skill", "s", nil, "Specific skill name(s)")
	cmd.Flags().BoolVar(&flagAll, "all", false, "Install all skills found in the repository")
	cmd.Flags().StringVar(&flagPath, "path", "", "Relative path within repo")
	cmd.Flags().StringVar(&flagURL, "url", "", "Custom Git clone URL")
	cmd.Flags().StringVar(&flagBranch, "branch", "", "Git branch or tag")
	cmd.Flags().StringSliceVarP(&flagAgents, "agent", "a", nil, "Target agents to link skill to")
	cmd.Flags().StringVar(&flagSymlink, "symlink", "", "Path to local skill directory for symlink install")
	cmd.Flags().StringVar(&flagCommand, "command", "", "Command to install the skill")
	cmd.Flags().StringVar(&flagCheck, "check", "", "Command to check before installing command skill")
	cmd.Flags().StringVar(&flagDescription, "description", "", "Description of the skill")
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip confirmation prompts")

	return cmd
}
