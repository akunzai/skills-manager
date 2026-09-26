package cli

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/presentation"
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
	source     engine.AddSource
	discovered engine.DiscoveredSkills
	labels     sourceLabels
	// progressLine is the row a Skill shows while Add applies it: plain text
	// on one line, since the progress region styles and truncates it.
	progressLine func(name, subpath string) string
}

type addRequest struct {
	// scope is where the Skills are declared, settled before the Source was
	// read (resolveAddScope).
	scope  Scope
	all    bool
	skills []string
	yes    bool
	agents []string
}

// resolveSkillsToAdd turns --all/--skill or an interactive prompt
// into the set of Skills to Add. Backing out is errAddCancelled; noneChosen
// reports choosing no Skills, which the caller must treat as a successful
// outcome, not an error.
func resolveSkillsToAdd(
	cmd *cobra.Command,
	discovered engine.DiscoveredSkills,
	intake *addIntake,
	flagAll bool,
	flagSkills []string,
	prompter addPrompter,
	interactive bool,
	skillsDir string,
) (skillsToAdd map[string]string, noneChosen bool, err error) {
	out := cmd.OutOrStdout()
	labels := intake.labels
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
			if outcome.CancelReason != engine.AddSelectionEmpty {
				return nil, false, errAddCancelled
			}
			fmt.Fprintf(out, "%sNo skills selected. Aborted.%s\n", colorYellow, colorReset)
			return nil, true, nil
		case engine.AddSelectionNeedsPath:
			if !interactive {
				return nil, false, fmt.Errorf("duplicate Skill %q requires a Source path: %s; specify a discovery scope with --path <directory> or a repository tree URL", outcome.Skill, strings.Join(outcome.Options, ", "))
			}
			chosen, promptErr := prompter.SelectSourcePath(outcome.Skill, outcome.Options)
			if errors.Is(promptErr, errAddCancelled) {
				answers.CancelReason = engine.AddSelectionUserCancelled
				continue
			}
			if promptErr != nil {
				return nil, false, promptErr
			}
			answers.Paths[outcome.Skill] = chosen
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

			var flat []tui.SelectOption
			if shouldGroup {
				for _, options := range groups {
					markInstalledSkills(options, skillsDir)
				}
			} else {
				groups = nil
				options := make([]tui.SelectOption, 0, len(outcome.Options))
				for _, skName := range outcome.Options {
					paths := discovered[skName]
					extra := ""
					if len(paths) > 1 {
						extra = fmt.Sprintf("(%d source paths)", len(paths))
					}
					options = append(options, tui.SelectOption{Key: skName, Title: skName, Extra: extra})
				}
				markInstalledSkills(options, skillsDir)
				slices.SortFunc(options, func(a, b tui.SelectOption) int {
					return cmp.Compare(a.Key, b.Key)
				})
				flat = options
			}
			chosen, promptErr := prompter.SelectSkills(fmt.Sprintf("Select skills to add from %s:", labels.displayName), groups, flat)
			if errors.Is(promptErr, errAddCancelled) {
				answers.CancelReason = engine.AddSelectionUserCancelled
				continue
			}
			if promptErr != nil {
				return nil, false, promptErr
			}
			answers.Skills = chosen
		}
	}
}

// run selects Skills, confirms replacements, then declares, Materializes,
// and applies Availability via BuildAddPlan and ApplyAddPlan. Backing out of
// any question is the user's choice rather than a failure, so it ends here,
// before anything is written, and succeeds (ADR-0002).
func (intake *addIntake) run(cmd *cobra.Command, req addRequest) error {
	return endAdd(cmd.OutOrStdout(), intake.add(cmd, req))
}

// endAdd is how Add ends when err is the user backing out of a question,
// the Scope asked before the Source is read included: a success (ADR-0002).
func endAdd(out io.Writer, err error) error {
	if errors.Is(err, errAddCancelled) {
		fmt.Fprintf(out, "%sOperation cancelled.%s\n", colorYellow, colorReset)
		return nil
	}
	return err
}

func (intake *addIntake) add(cmd *cobra.Command, req addRequest) error {
	out := cmd.OutOrStdout()
	// One answer to "may Add ask?" for every question below: --yes and a
	// missing terminal both mean take the defaults or fail where there is no
	// default.
	prompter := newAddPrompter(cmd)
	interactive := prompter.Interactive() && !req.yes

	skillsToAdd, noneChosen, err := resolveSkillsToAdd(cmd, intake.discovered, intake, req.all, req.skills, prompter, interactive, req.scope.SkillsDir)
	if err != nil {
		return err
	}
	if noneChosen {
		return nil
	}
	if len(skillsToAdd) == 0 {
		return fmt.Errorf("no matching skills to add")
	}

	scope := req.scope
	cfg, agents, err := prepareAddTarget(scope, req.agents)
	if err != nil {
		return err
	}
	configPath, skillsDir := scope.ConfigPath, scope.SkillsDir
	req.agents = agents
	intent, err := promptAddAvailability(cfg, skillsToAdd, skillsDir, prompter, interactive, req.agents)
	if err != nil {
		return err
	}

	plan := engine.BuildAddPlan(cfg, configPath, skillsDir, intake.source, skillsToAdd, intent)

	if len(plan.Conflicts) > 0 && !req.yes {
		if !interactive {
			return fmt.Errorf("refusing to overwrite %d existing skill(s) without a terminal; rerun with --yes", len(plan.Conflicts))
		}
		if err := prompter.ConfirmOverwrite(plan.Conflicts); err != nil {
			return err
		}
	}

	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		return err
	}

	region := presentation.StartRegion(cmd.ErrOrStderr(), "Adding "+countOf(len(plan.Skills), "Skill"), len(plan.Skills))
	result, err := engine.ApplyAddPlan(plan, cfg, func(ev engine.AddSkillEvent) {
		switch ev.Outcome {
		case "":
			region.Start(presentation.Job{Name: ev.Name, Label: intake.progressLine(ev.Name, ev.Subpath)})
		case engine.SyncDone:
			region.Done(ev.Name)
		default:
			// reportAddOutcome says why, once the region is gone.
			region.Fail(ev.Name)
		}
	})
	region.Stop()
	if err != nil {
		return err
	}
	return reportAddOutcome(out, result, filepath.Base(configPath), scopeFlagOf(scope))
}

// reportAddOutcome says why any Skill could not be applied, in Sync's words,
// then sums up. Add has already declared every Skill, so it adopts ADR-0002's
// codes: a blocked Skill leaves the Scope not matching its Config (1), a
// failed one is work that broke (2).
func reportAddOutcome(out io.Writer, result engine.AddResult, configName, scopeFlag string) error {
	for _, ev := range result.Events {
		if !syncEventIsProgress(ev.Kind) {
			printSyncEvent(out, ev)
		}
	}
	if result.StateError != "" {
		printScopeStateUnreadable(out, result.StateError)
	}
	if result.StateWarning != "" {
		printScopeStateWarning(out, result.StateWarning, scopeFlag)
	}
	added := fmt.Sprintf("Added %d skill(s) [%s]", len(result.AddedSkills), strings.Join(result.AddedSkills, ", "))
	if result.Blocked == 0 && result.Failed == 0 {
		fmt.Fprintf(out, "%s%s and updated %s.%s\n", colorGreen, added, configName, colorReset)
		return nil
	}
	var parts []string
	if result.Blocked > 0 {
		parts = append(parts, fmt.Sprintf("%d blocked", result.Blocked))
	}
	if result.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", result.Failed))
	}
	fmt.Fprintf(out, "%s%s to %s; %s.%s\n", colorYellow, added, configName, strings.Join(parts, ", "), colorReset)
	// Sync cannot get past an unreadable Scope state either, so it is only
	// the next step for a Skill that was blocked or failed on its own.
	if result.StateError == "" || result.Blocked+result.Failed > 1 {
		fmt.Fprintf(out, "Next: follow the reason given for each skill above, then run 'skills sync'.\n")
	}
	if result.Failed > 0 {
		return exitError{message: fmt.Sprintf("Add did not complete: %s, %s", countOf(result.Failed, "failure"), countOf(result.Blocked, "blocked skill")), code: 2}
	}
	return exitError{message: "Scope does not match its Config", code: 1}
}
