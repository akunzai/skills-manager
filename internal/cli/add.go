package cli

import (
	"fmt"
	"io"
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

const repositoryRootGroup = "Repository root"

func shouldPromptForDiscoveredSkills(skillCount int, interactive, skipConfirmation bool) bool {
	return skillCount > 0 && interactive && !skipConfirmation
}

func groupDiscoveredSkills(discovered map[string]string) (tui.GroupedItems, bool) {
	byDirectory := make(map[string][]tui.SelectOption)
	for name, skillPath := range discovered {
		directory := filepath.ToSlash(filepath.Dir(skillPath))
		byDirectory[directory] = append(byDirectory[directory], tui.SelectOption{Key: name, Title: name})
	}
	if len(byDirectory) <= 1 {
		return nil, false
	}

	directories := make([]string, 0, len(byDirectory))
	for directory := range byDirectory {
		directories = append(directories, directory)
	}
	sort.Strings(directories)

	labels := groupLabels(directories)
	groups := make(tui.GroupedItems, len(directories))
	for _, directory := range directories {
		options := byDirectory[directory]
		sort.Slice(options, func(i, j int) bool {
			return options[i].Key < options[j].Key
		})
		groups[labels[directory]] = options
	}

	return groups, true
}

func groupLabels(directories []string) map[string]string {
	depths := make(map[string]int, len(directories))
	for _, directory := range directories {
		depths[directory] = 1
	}

	for {
		labels := make(map[string]string, len(directories))
		duplicates := make(map[string][]string)
		for _, directory := range directories {
			label := repositoryRootGroup
			if directory != "." {
				parts := strings.Split(directory, "/")
				depth := depths[directory]
				if depth > len(parts) {
					depth = len(parts)
				}
				label = strings.Join(parts[len(parts)-depth:], "/")
				if label == repositoryRootGroup {
					label = "./" + label
				}
			}
			labels[directory] = label
			duplicates[label] = append(duplicates[label], directory)
		}

		resolved := true
		for _, directories := range duplicates {
			if len(directories) < 2 {
				continue
			}
			resolved = false
			for _, directory := range directories {
				depths[directory]++
			}
		}
		if resolved {
			return labels
		}
	}
}

func discoveryResult(discovered map[string]string, err error, sourceKey string) (map[string]string, error) {
	if err != nil {
		return nil, fmt.Errorf("discover skills in %s: %w", sourceKey, err)
	}
	if len(discovered) == 0 {
		return nil, fmt.Errorf("no SKILL.md found in %s", sourceKey)
	}
	return discovered, nil
}

func isLocalPath(raw string) bool {
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "~") ||
		strings.HasPrefix(raw, "./") || strings.HasPrefix(raw, "../") ||
		strings.HasPrefix(raw, `.\`) || strings.HasPrefix(raw, `..\`) {
		return true
	}
	// Windows drive letter paths: e.g. C:\foo or C:/foo
	if len(raw) >= 3 && ((raw[0] >= 'a' && raw[0] <= 'z') || (raw[0] >= 'A' && raw[0] <= 'Z')) && raw[1] == ':' && (raw[2] == '/' || raw[2] == '\\') {
		return true
	}
	if !strings.HasPrefix(raw, "git@") && !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") &&
		!strings.HasPrefix(raw, "github:") && !strings.HasPrefix(raw, "gitlab:") {
		expanded := models.ExpandUser(raw)
		if info, err := os.Stat(expanded); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

type conflictItem struct {
	name       string
	currentSrc string
	newSrc     string
}

func confirmSkillReplacements(
	cfg *config.Config,
	skillsDir string,
	skillsToInstall map[string]string,
	newSourceKey string,
	isLocal bool,
	absSourcePath string,
	flagYes bool,
	out io.Writer,
) error {
	var conflicts []conflictItem
	for name, subpath := range skillsToInstall {
		var newSrcDisplay string
		var localSkillSource string
		if isLocal {
			localSkillSource = absSourcePath
			if subpath != "" && subpath != "." {
				localSkillSource = filepath.Join(absSourcePath, filepath.FromSlash(subpath))
			}
			newSrcDisplay = fmt.Sprintf("[symlink] %s", models.ToTildePath(localSkillSource))
		} else {
			newSrcDisplay = fmt.Sprintf("[remote] %s (%s)", newSourceKey, subpath)
		}

		cat, srcKey, found := config.FindSkillSource(cfg, name)
		if found {
			if cat == "remote" {
				if isLocal || srcKey != newSourceKey {
					conflicts = append(conflicts, conflictItem{
						name:       name,
						currentSrc: fmt.Sprintf("[remote] %s", srcKey),
						newSrc:     newSrcDisplay,
					})
				}
			} else if cat == "local" {
				if entry, ok := cfg.Local[name]; ok {
					if entry.Type == "command" {
						conflicts = append(conflicts, conflictItem{
							name:       name,
							currentSrc: fmt.Sprintf("[command] %s", entry.Command),
							newSrc:     newSrcDisplay,
						})
					} else {
						if !isLocal || (entry.Source != StoreLocalSourcePath(localSkillSource, skillsDir) && models.ToTildePath(entry.Source) != models.ToTildePath(localSkillSource)) {
							conflicts = append(conflicts, conflictItem{
								name:       name,
								currentSrc: fmt.Sprintf("[symlink] %s", models.ToTildePath(entry.Source)),
								newSrc:     newSrcDisplay,
							})
						}
					}
				}
			}
		} else {
			targetPath := filepath.Join(skillsDir, name)
			if fi, err := os.Lstat(targetPath); err == nil {
				current := "[untracked directory]"
				if fi.Mode()&os.ModeSymlink != 0 {
					if target, err := os.Readlink(targetPath); err == nil {
						current = fmt.Sprintf("[symlink] %s", models.ToTildePath(target))
					} else {
						current = "[symlink]"
					}
				}
				conflicts = append(conflicts, conflictItem{
					name:       name,
					currentSrc: current,
					newSrc:     newSrcDisplay,
				})
			}
		}
	}

	if len(conflicts) > 0 && !flagYes {
		if !tui.IsTerminal() {
			return fmt.Errorf("refusing to overwrite %d existing skill(s) without a terminal; rerun with --yes", len(conflicts))
		}
		sort.Slice(conflicts, func(i, j int) bool {
			return conflicts[i].name < conflicts[j].name
		})
		fmt.Fprintf(out, "\n%s⚠️  The following %d skill(s) already exist and will be overwritten:%s\n", colorYellow, len(conflicts), colorReset)
		for _, c := range conflicts {
			fmt.Fprintf(out, "  • %s%s%s: %s -> %s\n", colorBold, c.name, colorReset, c.currentSrc, c.newSrc)
		}
		fmt.Fprintln(out)
		confirmed, err := tui.PromptConfirm("Do you want to proceed with overwriting these skills?", false)
		if err != nil {
			return err
		}
		if !confirmed {
			return fmt.Errorf("operation cancelled by user")
		}
	}
	return nil
}

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

			// 1. Local Symlink Mode (via --symlink flag or positional local path)
			if flagSymlink != "" || (flagCommand == "" && len(args) > 0 && isLocalPath(args[0])) {
				localPath := flagSymlink
				if localPath == "" {
					localPath = args[0]
				}

				sourcePath := models.ExpandUser(localPath)
				absSourcePath, err := filepath.Abs(sourcePath)
				if err != nil {
					absSourcePath = sourcePath
				}
				stat, err := os.Stat(absSourcePath)
				if err != nil || !stat.IsDir() {
					return fmt.Errorf("local source path does not exist or is not a directory: %s", sourcePath)
				}

				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "%s🔍 Scanning local directory: %s%s%s...\n", colorCyan, colorBold, models.ToTildePath(absSourcePath), colorReset)

				discovered, err := engine.DiscoverSkillsInRepo(absSourcePath)
				if err != nil {
					return fmt.Errorf("discover skills in %s: %w", sourcePath, err)
				}
				if len(discovered) == 0 {
					if _, err := os.Stat(filepath.Join(absSourcePath, "SKILL.md")); err == nil {
						skillName := engine.ParseSkillNameFromMD(filepath.Join(absSourcePath, "SKILL.md"))
						if skillName == "" {
							skillName = filepath.Base(absSourcePath)
						}
						discovered[skillName] = "."
					} else {
						return fmt.Errorf("no SKILL.md found in %s", sourcePath)
					}
				}

				skillsToInstall := make(map[string]string)

				if flagAll {
					skillsToInstall = discovered
				} else if flagPath != "" && len(flagSkills) == 0 {
					cleanSub := filepath.ToSlash(strings.Trim(flagPath, "/"))
					for k, v := range discovered {
						if filepath.ToSlash(strings.Trim(v, "/")) == cleanSub {
							skillsToInstall[k] = v
						}
					}
					if len(skillsToInstall) == 0 {
						subDir := filepath.Join(absSourcePath, filepath.FromSlash(flagPath))
						if _, err := os.Stat(filepath.Join(subDir, "SKILL.md")); err == nil {
							skillsToInstall[filepath.Base(subDir)] = flagPath
						} else {
							return fmt.Errorf("specified path '%s' does not contain SKILL.md", flagPath)
						}
					}
				} else if len(flagSkills) > 0 {
					if len(discovered) == 1 && len(flagSkills) == 1 {
						for _, sub := range discovered {
							skillsToInstall[flagSkills[0]] = sub
						}
					} else {
						for _, sk := range flagSkills {
							if sub, ok := discovered[sk]; ok {
								skillsToInstall[sk] = sub
							} else if flagPath != "" {
								skillsToInstall[sk] = flagPath
							} else {
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
									fmt.Fprintf(out, "%sWarning: Skill '%s' not found in discovered list (%s)%s\n", colorYellow, sk, strings.Join(discNames, ", "), colorReset)
								}
							}
						}
					}
				} else {
					if shouldPromptForDiscoveredSkills(len(discovered), tui.IsTerminal(), flagYes) {
						groups, shouldGroup := groupDiscoveredSkills(discovered)
						if shouldGroup {
							for _, options := range groups {
								for i := range options {
									if _, err := os.Stat(filepath.Join(skillsDir, options[i].Key)); err == nil {
										options[i].Selected = true
									}
								}
							}
						}

						var chosen []string
						if shouldGroup {
							chosen, err = tui.PromptGroupedMultiSelect(
								fmt.Sprintf("Select skills to link from %s:", models.ToTildePath(absSourcePath)),
								groups,
							)
						} else {
							options := make([]tui.SelectOption, 0, len(discovered))
							for skName := range discovered {
								isInst := false
								if _, err := os.Stat(filepath.Join(skillsDir, skName)); err == nil {
									isInst = true
								}
								options = append(options, tui.SelectOption{Key: skName, Title: skName, Selected: isInst})
							}
							sort.Slice(options, func(i, j int) bool {
								return options[i].Key < options[j].Key
							})
							chosen, err = tui.PromptMultiSelect(
								fmt.Sprintf("Select skills to link from %s:", models.ToTildePath(absSourcePath)),
								options,
							)
						}
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
						for _, ch := range chosen {
							skillsToInstall[ch] = discovered[ch]
						}
					} else if len(discovered) == 1 {
						skillsToInstall = discovered
					} else {
						discNames := make([]string, 0, len(discovered))
						for k := range discovered {
							discNames = append(discNames, k)
						}
						sort.Strings(discNames)
						fmt.Fprintf(out, "%sLocal directory contains multiple skills:%s %s\n", colorYellow, colorReset, strings.Join(discNames, ", "))
						fmt.Fprintf(out, "Please specify --skill <name> or --all\n")
						return fmt.Errorf("multiple skills found without selection")
					}
				}

				if len(skillsToInstall) == 0 {
					return fmt.Errorf("no matching skills to install")
				}

				if err := confirmSkillReplacements(cfg, skillsDir, skillsToInstall, "", true, absSourcePath, flagYes, out); err != nil {
					if err.Error() == "operation cancelled by user" {
						fmt.Fprintf(out, "%sOperation cancelled.%s\n", colorYellow, colorReset)
						return nil
					}
					return err
				}

				if err := os.MkdirAll(skillsDir, 0755); err != nil {
					return err
				}

				var installedNames []string
				for name, subpath := range skillsToInstall {
					skillSource := absSourcePath
					if subpath != "" && subpath != "." {
						skillSource = filepath.Join(absSourcePath, filepath.FromSlash(subpath))
					}
					destLink := filepath.Join(skillsDir, name)
					linkTarget := LocalSymlinkTarget(skillSource, skillsDir)

					fmt.Fprintf(out, "  🔗 Linking local skill: %s%s%s -> %s\n", colorBold, name, colorReset, models.ToTildePath(skillSource))
					if err := engine.CreateSymlink(linkTarget, destLink, true); err != nil {
						return fmt.Errorf("failed to symlink skill %s: %w", name, err)
					}

					targetAgents := flagAgents
					if len(targetAgents) == 0 {
						targetAgents = engine.GetTargetAgentsForSkill(name, "local", cfg, skillsDir)
					}
					for _, agent := range targetAgents {
						_, _ = engine.EnsureAgentSymlink(name, agent, skillsDir)
					}

					config.AddLocalSymlinkEntry(cfg, name, StoreLocalSourcePath(skillSource, skillsDir), flagDescription)
					installedNames = append(installedNames, name)
				}

				if err := config.SaveConfig(cfg, configPath); err != nil {
					return err
				}

				sort.Strings(installedNames)
				fmt.Fprintf(out, "\n%s✔ Successfully linked %d local skill(s) [%s] and updated %s%s\n\n", colorGreen, len(installedNames), strings.Join(installedNames, ", "), filepath.Base(configPath), colorReset)
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
			discovered, err = discoveryResult(discovered, err, sourceKey)
			if err != nil {
				return err
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
				if shouldPromptForDiscoveredSkills(len(discovered), tui.IsTerminal(), flagYes) {
					groups, shouldGroup := groupDiscoveredSkills(discovered)
					if shouldGroup {
						for _, options := range groups {
							for i := range options {
								if _, err := os.Stat(filepath.Join(skillsDir, options[i].Key)); err == nil {
									options[i].Selected = true
								}
							}
						}
					}

					var chosen []string
					if shouldGroup {
						chosen, err = tui.PromptGroupedMultiSelect(
							fmt.Sprintf("Select skills to install from %s:", sourceKey),
							groups,
						)
					} else {
						options := make([]tui.SelectOption, 0, len(discovered))
						for skName := range discovered {
							isInst := false
							if _, err := os.Stat(filepath.Join(skillsDir, skName)); err == nil {
								isInst = true
							}
							options = append(options, tui.SelectOption{Key: skName, Title: skName, Selected: isInst})
						}
						sort.Slice(options, func(i, j int) bool {
							return options[i].Key < options[j].Key
						})
						chosen, err = tui.PromptMultiSelect(
							fmt.Sprintf("Select skills to install from %s:", sourceKey),
							options,
						)
					}
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
				} else if len(discovered) == 1 {
					skillsToInstall = discovered
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

			if err := confirmSkillReplacements(cfg, skillsDir, skillsToInstall, sourceKey, false, "", flagYes, cmd.OutOrStdout()); err != nil {
				if err.Error() == "operation cancelled by user" {
					fmt.Fprintf(cmd.OutOrStdout(), "%sOperation cancelled.%s\n", colorYellow, colorReset)
					return nil
				}
				return err
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
