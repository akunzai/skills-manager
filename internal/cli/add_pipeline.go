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

	"github.com/akunzai/skills-manager/internal/config"
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
	scope Scope
	// cfg is the Scope's Config, loaded once for the whole Add.
	cfg    *config.Config
	all    bool
	skills []string
	yes    bool
	agents []string
	// trustCert is --trust-cert as Config stores it.
	trustCert string
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
	occupancy func(name, subpath string) (engine.AddSlot, error),
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

			slots := make(map[string]engine.AddSlot, len(outcome.Options))
			if occupancy != nil {
				for _, name := range outcome.Options {
					paths := discovered[name]
					slot, err := occupancy(name, paths[0])
					if err != nil {
						return nil, false, err
					}
					// Ask early only when choosing a path changes the occupancy badge.
					// Otherwise keep Source-path selection after Skill selection.
					for _, path := range paths[1:] {
						other, err := occupancy(name, path)
						if err != nil {
							return nil, false, err
						}
						if other.Occupancy == slot.Occupancy {
							continue
						}
						chosen, err := prompter.SelectSourcePath(name, paths)
						if err != nil {
							return nil, false, err
						}
						if !slices.Contains(paths, chosen) {
							return nil, false, fmt.Errorf("Source path %q is not a candidate for Skill %q", chosen, name)
						}
						answers.Paths[name] = chosen
						slot, err = occupancy(name, chosen)
						if err != nil {
							return nil, false, err
						}
						break
					}
					slots[name] = slot
				}
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
					markOccupiedSkills(options, slots)
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
				markOccupiedSkills(options, slots)
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

	var occupancy func(name, subpath string) (engine.AddSlot, error)
	if interactive {
		occupancy = func(name, subpath string) (engine.AddSlot, error) {
			return engine.InspectAddSlot(req.cfg, req.scope.SkillsDir, intake.source, name, subpath)
		}
	}

	skillsToAdd, noneChosen, err := resolveSkillsToAdd(cmd, intake.discovered, intake, req.all, req.skills, prompter, interactive, occupancy)
	if err != nil {
		return err
	}
	if noneChosen {
		return nil
	}
	if len(skillsToAdd) == 0 {
		return fmt.Errorf("no matching skills to add")
	}

	scope, cfg := req.scope, req.cfg
	configPath, skillsDir := scope.ConfigPath, scope.SkillsDir
	if len(req.agents) > 0 {
		if req.agents, err = engine.NewAvailability(cfg, skillsDir).ValidateManagedAgents(req.agents); err != nil {
			return err
		}
	}
	intent, err := promptAddAvailability(cfg, skillsToAdd, skillsDir, prompter, interactive, req.agents)
	if err != nil {
		return err
	}

	plan, err := engine.BuildAddPlan(cfg, configPath, skillsDir, intake.source, skillsToAdd, intent)
	if err != nil {
		return err
	}
	plan.TrustCert = req.trustCert

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
	return reportAddOutcome(out, result, filepath.Base(configPath), scopeFlagsOf(cmd, scope))
}

// reportAddOutcome says why any Skill could not be applied, in Sync's words,
// then sums up with ADR-0002's code for the result's Convergence.
func reportAddOutcome(out io.Writer, result engine.AddResult, configName, scopeFlag string) error {
	for _, ev := range result.Events {
		if !syncEventIsProgress(ev.Kind) {
			printSyncEvent(out, ev, scopeFlag)
		}
	}
	if result.State.Verdict == engine.StateFail {
		printScopeStateUnreadable(out, result.State.Message)
	}
	if result.State.Verdict == engine.StateWarn {
		printScopeStateWarning(out, result.State.Message, scopeFlag)
	}
	added := fmt.Sprintf("Added %d skill(s) [%s]", len(result.AddedSkills), strings.Join(result.AddedSkills, ", "))
	convergence := result.Convergence()
	if convergence == engine.Converged {
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
	if result.State.Verdict == engine.StateFail {
		parts = append(parts, "Baselines not recorded")
	}
	fmt.Fprintf(out, "%s%s to %s; %s.%s\n", colorYellow, added, configName, strings.Join(parts, ", "), colorReset)
	// A command carried by an outcome takes precedence over a generic retry.
	if !printNextCommands(out, scopeFlag, syncErrors(result.Events)...) && (result.State.Verdict != engine.StateFail || result.Blocked+result.Failed > 0) {
		fmt.Fprintf(out, "Next: follow the reason given for each skill above, then run 'skills sync%s'.\n", scopeFlag)
	}
	if convergence == engine.Incomplete {
		message := fmt.Sprintf("Add did not complete: %s, %s", countOf(result.Failed, "failure"), countOf(result.Blocked, "blocked skill"))
		if result.State.Verdict == engine.StateFail {
			message += ", Baselines not recorded"
		}
		return exitError{message: message, code: 2}
	}
	return exitError{message: "Scope does not match its Config", code: 1}
}
