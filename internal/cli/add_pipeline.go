package cli

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

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
	source       engine.AddSource
	discovered   engine.DiscoveredSkills
	labels       sourceLabels
	progressLine func(name, subpath string) string
}

type addRequest struct {
	all    bool
	skills []string
	yes    bool
	agents []string
}

var addSelectionIsTerminal = tui.IsTerminal

var addPromptSourcePath = func(name string, paths []string) (string, error) {
	options := make([]tui.SelectOption, 0, len(paths)+1)
	options = append(options, tui.SelectOption{Title: "Select a Source path"})
	for _, candidate := range paths {
		options = append(options, tui.SelectOption{Key: candidate, Title: candidate})
	}
	return tui.PromptSelect(fmt.Sprintf("Select a Source path for %s:", name), options, -1)
}

// resolveSkillsToAdd turns --all/--skill or an interactive prompt
// into the set of Skills to Add. cancelled reports a user-cancelled
// selection, which the caller must treat as a successful outcome, not an error.
func resolveSkillsToAdd(
	cmd *cobra.Command,
	discovered engine.DiscoveredSkills,
	intake *addIntake,
	flagAll bool,
	flagSkills []string,
	flagYes bool,
) (skillsToAdd map[string]string, cancelled bool, err error) {
	out := cmd.OutOrStdout()
	labels := intake.labels
	interactive := addSelectionIsTerminal() && !flagYes
	request := engine.AddSelectionRequest{All: flagAll, Skills: flagSkills}
	answers := engine.AddSelectionAnswers{Paths: make(map[string]string)}

	for {
		outcome, resolveErr := engine.ResolveAddSelection(discovered, request, answers)
		if resolveErr != nil {
			return nil, false, resolveErr
		}
		switch outcome.Kind {
		case engine.AddSelectionResolved:
			return outcome.Skills, false, nil
		case engine.AddSelectionCancelled:
			if outcome.CancelReason == engine.AddSelectionEmpty {
				fmt.Fprintf(out, "%sNo skills selected. Aborted.%s\n", colorYellow, colorReset)
			} else {
				fmt.Fprintf(out, "%sOperation cancelled.%s\n", colorYellow, colorReset)
			}
			return nil, true, nil
		case engine.AddSelectionNeedsPath:
			if !interactive {
				return nil, false, fmt.Errorf("duplicate Skill %q requires a Source path: %s; specify a discovery scope with --path <directory> or a repository tree URL", outcome.Skill, strings.Join(outcome.Options, ", "))
			}
			chosen, promptErr := addPromptSourcePath(outcome.Skill, outcome.Options)
			if promptErr != nil {
				return nil, false, promptErr
			}
			if chosen == "" {
				answers.CancelReason = engine.AddSelectionUserCancelled
			} else {
				answers.Paths[outcome.Skill] = chosen
			}
		case engine.AddSelectionNeedsSkills:
			if !interactive {
				fmt.Fprintf(out, "%s%s contains multiple skills:%s %s\n", colorYellow, labels.resourceNoun, colorReset, strings.Join(outcome.Options, ", "))
				fmt.Fprintln(out, "Please specify --skill <name> or --all")
				return nil, false, fmt.Errorf("multiple skills found without selection")
			}

			displayPaths := make(map[string]string, len(discovered))
			shouldGroup := true
			for name, paths := range discovered {
				if len(paths) != 1 {
					shouldGroup = false
					break
				}
				displayPaths[name] = paths[0]
			}
			groups := tui.GroupedItems(nil)
			if shouldGroup {
				groups, shouldGroup = groupDiscoveredSkills(displayPaths)
			}
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
				options := make([]tui.SelectOption, 0, len(outcome.Options))
				for _, skName := range outcome.Options {
					paths := discovered[skName]
					extra := ""
					if len(paths) > 1 {
						extra = fmt.Sprintf("(%d source paths)", len(paths))
					}
					options = append(options, tui.SelectOption{Key: skName, Title: skName, Extra: extra})
				}
				markInstalledSkills(options, selectionDirs)
				slices.SortFunc(options, func(a, b tui.SelectOption) int {
					return cmp.Compare(a.Key, b.Key)
				})
				chosen, promptErr = tui.PromptMultiSelect(promptTitle, options)
			}
			if promptErr != nil {
				return nil, false, promptErr
			}
			if chosen == nil {
				answers.CancelReason = engine.AddSelectionUserCancelled
				continue
			}
			answers.Skills = chosen
		}
	}
}

// run selects Skills, confirms replacements, then declares, Materializes,
// and applies Availability via BuildAddPlan and ApplyAddPlan.
func (intake *addIntake) run(cmd *cobra.Command, req addRequest) error {
	out := cmd.OutOrStdout()

	skillsToAdd, cancelled, err := resolveSkillsToAdd(cmd, intake.discovered, intake, req.all, req.skills, req.yes)
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

	plan := engine.BuildAddPlan(cfg, configPath, skillsDir, intake.source, skillsToAdd, req.agents)

	if len(plan.Conflicts) > 0 && !req.yes {
		if err := promptConfirmConflicts(out, plan.Conflicts); err != nil {
			if err.Error() == "operation cancelled by user" {
				fmt.Fprintf(out, "%sOperation cancelled.%s\n", colorYellow, colorReset)
				return nil
			}
			return err
		}
	}

	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		return err
	}

	result, err := engine.ApplyAddPlan(plan, cfg, func(ev engine.AddSkillEvent) {
		fmt.Fprintf(out, "  %s\n", intake.progressLine(ev.Name, ev.Subpath))
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "\n%sAdded %d skill(s) [%s] and updated %s.%s\n\n", colorGreen, len(result.AddedSkills), strings.Join(result.AddedSkills, ", "), filepath.Base(configPath), colorReset)
	return nil
}
