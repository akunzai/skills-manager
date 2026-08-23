package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/tui"
	"github.com/spf13/cobra"
)

// sourceLabels carries display text that differs between Sources.
type sourceLabels struct {
	displayName  string // shown in interactive prompt titles
	resourceNoun string // "Local directory" / "Repository", for the "contains multiple skills" message
}

// addIntake owns one acquired Source from discovery through selection and
// confirmation. Source-specific constructors are the only way to create it.
type addIntake struct {
	rootDir       string
	discovered    map[string]string
	labels        sourceLabels
	replacement   replacementSource
	allowRename   bool
	selectionPath string
	progressLine  func(name, subpath string) string
	newAdder      func(cfg *config.Config, configPath, skillsDir string) *engine.Adder
}

type addRequest struct {
	all    bool
	skills []string
	yes    bool
	agents []string
}

func sortedDiscoveredNames(discovered map[string]string) []string {
	names := make([]string, 0, len(discovered))
	for name := range discovered {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// resolveSkillsToAdd turns --all/--path/--skill or an interactive prompt
// into the set of Skills to Add. cancelled reports a user-cancelled
// selection, which the caller must treat as a silent success, not an error.
func resolveSkillsToAdd(
	cmd *cobra.Command,
	discovered map[string]string,
	intake *addIntake,
	flagAll bool,
	pathOverride string,
	flagSkills []string,
	flagYes bool,
) (skillsToAdd map[string]string, cancelled bool, err error) {
	out := cmd.OutOrStdout()
	labels := intake.labels
	skillsToAdd = make(map[string]string)

	switch {
	case flagAll:
		skillsToAdd = discovered

	case pathOverride != "" && len(flagSkills) == 0:
		cleanSub := filepath.ToSlash(strings.Trim(pathOverride, "/"))
		for k, v := range discovered {
			if filepath.ToSlash(strings.Trim(v, "/")) == cleanSub {
				skillsToAdd[k] = v
			}
		}
		if len(skillsToAdd) == 0 {
			subDir := filepath.Join(intake.rootDir, filepath.FromSlash(pathOverride))
			if _, statErr := os.Stat(filepath.Join(subDir, "SKILL.md")); statErr == nil {
				skillsToAdd[filepath.Base(subDir)] = pathOverride
			} else {
				return nil, false, fmt.Errorf("specified path '%s' does not contain SKILL.md", pathOverride)
			}
		}

	case len(flagSkills) > 0:
		if len(discovered) == 1 && len(flagSkills) == 1 && intake.allowRename {
			for _, sub := range discovered {
				skillsToAdd[flagSkills[0]] = sub
			}
		} else {
			for _, sk := range flagSkills {
				if sub, ok := discovered[sk]; ok {
					skillsToAdd[sk] = sub
				} else if pathOverride != "" {
					skillsToAdd[sk] = pathOverride
				} else {
					matched := false
					for k, v := range discovered {
						if strings.EqualFold(k, sk) {
							skillsToAdd[k] = v
							matched = true
							break
						}
					}
					if !matched {
						fmt.Fprintf(out, "%sWarning: Skill '%s' not found in discovered list (%s)%s\n", colorYellow, sk, strings.Join(sortedDiscoveredNames(discovered), ", "), colorReset)
					}
				}
			}
		}

	default:
		if shouldPromptForDiscoveredSkills(len(discovered), tui.IsTerminal(), flagYes) {
			groups, shouldGroup := groupDiscoveredSkills(discovered)
			selectionDirs := selectionSkillsDirs(cmd)

			var chosen []string
			var promptErr error
			promptTitle := fmt.Sprintf("Select skills to add from %s:", labels.displayName)
			if shouldGroup {
				for _, options := range groups {
					markInstalledSkills(options, selectionDirs)
				}
				chosen, promptErr = tui.PromptGroupedMultiSelect(promptTitle, groups)
			} else {
				options := make([]tui.SelectOption, 0, len(discovered))
				for skName := range discovered {
					options = append(options, tui.SelectOption{Key: skName, Title: skName})
				}
				markInstalledSkills(options, selectionDirs)
				sort.Slice(options, func(i, j int) bool {
					return options[i].Key < options[j].Key
				})
				chosen, promptErr = tui.PromptMultiSelect(promptTitle, options)
			}
			if promptErr != nil {
				return nil, false, promptErr
			}
			if chosen == nil {
				fmt.Fprintf(out, "%sOperation cancelled.%s\n", colorYellow, colorReset)
				return nil, true, nil
			}
			if len(chosen) == 0 {
				fmt.Fprintf(out, "%sNo skills selected. Aborted.%s\n", colorYellow, colorReset)
				return nil, true, nil
			}
			for _, ch := range chosen {
				skillsToAdd[ch] = discovered[ch]
			}
		} else if len(discovered) == 1 {
			skillsToAdd = discovered
		} else {
			fmt.Fprintf(out, "%s%s contains multiple skills:%s %s\n", colorYellow, labels.resourceNoun, colorReset, strings.Join(sortedDiscoveredNames(discovered), ", "))
			fmt.Fprintf(out, "Please specify --skill <name> or --all\n")
			return nil, false, fmt.Errorf("multiple skills found without selection")
		}
	}

	return skillsToAdd, false, nil
}

// run selects Skills, confirms replacements, then delegates declaration,
// Materialize, and Availability to the Source-specific Adder.
func (intake *addIntake) run(cmd *cobra.Command, req addRequest) error {
	out := cmd.OutOrStdout()

	skillsToAdd, cancelled, err := resolveSkillsToAdd(cmd, intake.discovered, intake, req.all, intake.selectionPath, req.skills, req.yes)
	if err != nil {
		return err
	}
	if cancelled {
		return nil
	}
	if len(skillsToAdd) == 0 {
		return fmt.Errorf("no matching skills to add")
	}

	configPath, skillsDir, cfg, agents, err := prepareAddTarget(cmd, req.yes, req.agents)
	if err != nil {
		return err
	}
	req.agents = agents
	if err := promptAddAvailability(cfg, skillsToAdd, skillsDir, req.yes, req.agents); err != nil {
		return err
	}

	if err := confirmSkillReplacements(cfg, skillsDir, skillsToAdd, intake.replacement, req.yes, out); err != nil {
		if err.Error() == "operation cancelled by user" {
			fmt.Fprintf(out, "%sOperation cancelled.%s\n", colorYellow, colorReset)
			return nil
		}
		return err
	}

	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		return err
	}

	if err := intake.newAdder(cfg, configPath, skillsDir).Run(skillsToAdd, req.agents, func(name, subpath string) {
		fmt.Fprintf(out, "  %s\n", intake.progressLine(name, subpath))
	}); err != nil {
		return err
	}

	addedNames := make([]string, 0, len(skillsToAdd))
	for name := range skillsToAdd {
		addedNames = append(addedNames, name)
	}
	sort.Strings(addedNames)
	fmt.Fprintf(out, "\n%sAdded %d skill(s) [%s] and updated %s.%s\n\n", colorGreen, len(addedNames), strings.Join(addedNames, ", "), filepath.Base(configPath), colorReset)
	return nil
}
