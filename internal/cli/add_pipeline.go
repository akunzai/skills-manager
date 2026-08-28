package cli

import (
	"cmp"
	"fmt"
	"maps"
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
	source        engine.AddSource
	rootDir       string
	discovered    engine.DiscoveredSkills
	labels        sourceLabels
	selectionPath string
	progressLine  func(name, subpath string) string
}

type addRequest struct {
	all    bool
	skills []string
	yes    bool
	agents []string
}

var addSelectionIsTerminal = tui.IsTerminal

var addPromptSourcePath = func(name string, paths []string) (string, error) {
	options := make([]tui.SelectOption, 0, len(paths))
	for _, candidate := range paths {
		options = append(options, tui.SelectOption{Key: candidate, Title: candidate})
	}
	return tui.PromptSelect(fmt.Sprintf("Select a Source path for %s:", name), options, -1)
}

func sortedDiscoveredNames(discovered engine.DiscoveredSkills) []string {
	return slices.Sorted(maps.Keys(discovered))
}

func flattenedDiscoveredSkills(discovered engine.DiscoveredSkills) map[string]string {
	flat := make(map[string]string, len(discovered))
	for name, paths := range discovered {
		if len(paths) == 1 {
			flat[name] = paths[0]
		} else {
			flat[name] = ""
		}
	}
	return flat
}

// resolveSkillsToAdd turns --all/--path/--skill or an interactive prompt
// into the set of Skills to Add. cancelled reports a user-cancelled
// selection, which the caller must treat as a silent success, not an error.
func resolveSkillsToAdd(
	cmd *cobra.Command,
	discovered engine.DiscoveredSkills,
	intake *addIntake,
	flagAll bool,
	pathOverride string,
	flagSkills []string,
	flagYes bool,
) (skillsToAdd map[string]string, cancelled bool, err error) {
	out := cmd.OutOrStdout()
	labels := intake.labels

	flat := flattenedDiscoveredSkills(discovered)
	resolved, unmatched, err := engine.ResolveDiscoveredSkills(flat, intake.rootDir, flagAll, pathOverride, flagSkills, intake.source.AllowRename)
	if err != nil {
		return nil, false, err
	}
	for _, sk := range unmatched {
		fmt.Fprintf(out, "%sWarning: Skill '%s' not found in discovered list (%s)%s\n", colorYellow, sk, strings.Join(sortedDiscoveredNames(discovered), ", "), colorReset)
	}
	if resolved != nil {
		return resolveCandidatePaths(resolved, discovered, addSelectionIsTerminal() && !flagYes)
	}

	if shouldPromptForDiscoveredSkills(len(discovered), addSelectionIsTerminal(), flagYes) {
		groups, shouldGroup := groupDiscoveredSkills(flat)
		for _, paths := range discovered {
			if len(paths) > 1 {
				shouldGroup = false
				break
			}
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
			options := make([]tui.SelectOption, 0, len(discovered))
			for skName, paths := range discovered {
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
			fmt.Fprintf(out, "%sOperation cancelled.%s\n", colorYellow, colorReset)
			return nil, true, nil
		}
		if len(chosen) == 0 {
			fmt.Fprintf(out, "%sNo skills selected. Aborted.%s\n", colorYellow, colorReset)
			return nil, true, nil
		}
		skillsToAdd = make(map[string]string, len(chosen))
		for _, ch := range chosen {
			skillsToAdd[ch] = flat[ch]
		}
	} else if len(discovered) == 1 {
		skillsToAdd = flat
	} else {
		fmt.Fprintf(out, "%s%s contains multiple skills:%s %s\n", colorYellow, labels.resourceNoun, colorReset, strings.Join(sortedDiscoveredNames(discovered), ", "))
		fmt.Fprintf(out, "Please specify --skill <name> or --all\n")
		return nil, false, fmt.Errorf("multiple skills found without selection")
	}

	return resolveCandidatePaths(skillsToAdd, discovered, addSelectionIsTerminal() && !flagYes)
}

func resolveCandidatePaths(selected map[string]string, discovered engine.DiscoveredSkills, interactive bool) (map[string]string, bool, error) {
	return resolveCandidatePathsWith(selected, discovered, interactive, addPromptSourcePath)
}

func resolveCandidatePathsWith(selected map[string]string, discovered engine.DiscoveredSkills, interactive bool, choose func(string, []string) (string, error)) (map[string]string, bool, error) {
	for _, name := range slices.Sorted(maps.Keys(selected)) {
		path := selected[name]
		if path != "" {
			continue
		}
		paths := discovered[name]
		if len(paths) == 0 && len(discovered) == 1 {
			for _, solePaths := range discovered {
				paths = solePaths
			}
		}
		if len(paths) < 2 {
			return nil, false, fmt.Errorf("no Source path found for Skill %q", name)
		}
		if !interactive {
			return nil, false, fmt.Errorf("duplicate Skill %q requires a Source path: %s; specify a discovery scope with --path <directory> or a repository tree URL", name, strings.Join(paths, ", "))
		}
		chosen, err := choose(name, paths)
		if err != nil {
			return nil, false, err
		}
		if chosen == "" {
			return nil, true, nil
		}
		selected[name] = chosen
	}
	return selected, false, nil
}

// run selects Skills, confirms replacements, then declares, Materializes,
// and applies Availability via BuildAddPlan and ApplyAddPlan.
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
