package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/akunzai/skills-manager/internal/tui"
	"github.com/spf13/cobra"
)

type adoptOptions struct {
	all    bool
	yes    bool
	dryRun bool
	to     string
}

func newAdoptCmd() *cobra.Command {
	var options adoptOptions
	cmd := &cobra.Command{
		Use:   "adopt [skills...]",
		Short: "Declare untracked skills in Config, keeping what is on disk",
		Long: `Declare Untracked Skills on the Scope skills directory, the inverse of prune.

A Skill an installer lock file records is declared from that remote Source and
stays where it is. Any other Skill, and any Skill that is its own git checkout,
moves to skills-local beside the skills directory (or --to) and is declared as
a local Source there.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if options.all && len(args) > 0 {
				return fmt.Errorf("--all cannot be combined with named Skills")
			}
			cmd.SilenceUsage = true
			return runAdopt(cmd, args, options)
		},
	}
	cmd.Flags().BoolVar(&options.all, "all", false, "Adopt every untracked skill")
	cmd.Flags().BoolVarP(&options.yes, "yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().BoolVar(&options.dryRun, "dry-run", false, "Show what would be adopted without changing files")
	cmd.Flags().StringVar(&options.to, "to", "", "Directory to move skills with no known Source into (default: skills-local beside the skills directory)")
	return cmd
}

func runAdopt(cmd *cobra.Command, names []string, options adoptOptions) error {
	out := cmd.OutOrStdout()
	scope := ResolveScope()
	cfg, err := config.LoadConfig(scope.ConfigPath)
	if err != nil {
		return err
	}
	plan, err := engine.BuildAdoptPlan(cfg, scope, engine.AdoptOptions{
		LockPath: engine.InstallerLockPath(scope.SkillsDir),
		MoveDir:  options.to,
	})
	if err != nil {
		return fmt.Errorf("build adopt plan: %w", err)
	}
	if unknown := plan.Unknown(names); len(unknown) > 0 {
		return fmt.Errorf("not an untracked skill in %s: %s", models.ToTildePath(scope.SkillsDir), strings.Join(unknown, ", "))
	}
	if len(plan.Items) == 0 {
		fmt.Fprintln(out, "Nothing to adopt.")
		return nil
	}
	if len(names) > 0 {
		plan = plan.Select(names)
	}
	if options.dryRun {
		printAdoptPlan(out, plan, scope.SkillsDir)
		fmt.Fprintln(out, "Dry run complete.")
		return nil
	}

	chosen := len(names) > 0 || options.all
	switch {
	case !chosen && options.yes:
		// Moving a real directory needs the user to name it, as prune needs
		// one selected before it removes it.
		printAdoptPlan(out, plan, scope.SkillsDir)
		fmt.Fprintf(out, "Skipped %s.\n", countOf(len(plan.Items), "untracked skill"))
		fmt.Fprintf(out, "Next: name the skills to adopt, or pass --all.\n")
		return exitError{message: "untracked skills were left as they are", code: 1}
	case !options.yes && !tui.IsTerminal():
		printAdoptPlan(out, plan, scope.SkillsDir)
		return fmt.Errorf("refusing to adopt without a terminal; rerun with --yes or --dry-run")
	case !chosen:
		selected, err := promptAdoptPlan(plan)
		if err != nil {
			return err
		}
		if selected == nil {
			fmt.Fprintln(out, "Operation cancelled.")
			return nil
		}
		if len(selected) == 0 {
			fmt.Fprintln(out, "No skills selected. Aborted.")
			return nil
		}
		plan = plan.Select(selected)
	case !options.yes:
		printAdoptPlan(out, plan, scope.SkillsDir)
		ok, err := tui.PromptConfirm(fmt.Sprintf("Adopt %s?", countOf(len(plan.Items), "skill")), false)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(out, "Operation cancelled.")
			return nil
		}
	default:
		printAdoptPlan(out, plan, scope.SkillsDir)
	}

	return reportAdoptOutcome(out, engine.ApplyAdoptPlan(plan, cfg, scope), scope)
}

// adoptAction words what adopt does, or would do, with one Skill.
func adoptAction(item engine.AdoptItem) string {
	if item.Block != "" {
		return "left in place: " + item.Block
	}
	if item.Action == engine.AdoptDeclareRemote {
		return "declare from " + adoptSource(item.Record.Source, item.Record.Subpath, item.Record.Branch)
	}
	action := "move to " + models.ToTildePath(item.MoveTo)
	if item.GitCheckout {
		action += " with its .git"
	}
	return action + ", then declare it as a local Source"
}

func adoptSource(source, subpath, branch string) string {
	if branch != "" {
		source += "@" + branch
	}
	if subpath != "" {
		source += " (" + subpath + ")"
	}
	return source
}

func printAdoptPlan(out io.Writer, plan engine.AdoptPlan, skillsDir string) {
	fmt.Fprintf(out, "Adopt plan: %s in %s.\n", countOf(len(plan.Items), "untracked skill"), models.ToTildePath(skillsDir))
	var recorded []string
	for _, item := range plan.Items {
		fmt.Fprintf(out, "  %s: %s\n", item.Name, adoptAction(item))
		if item.Recorded {
			recorded = append(recorded, item.Name)
		}
	}
	if len(plan.NotSkills) > 0 {
		fmt.Fprintf(out, "  Not a skill (no SKILL.md), left in place: %s\n", strings.Join(plan.NotSkills, ", "))
	}
	if plan.LockError != "" {
		fmt.Fprintf(out, "%sWarning: %s; no skill is treated as recorded by it.%s\n", colorYellow, plan.LockError, colorReset)
	}
	printInstallerWarning(out, recorded, skillsDir)
}

// printInstallerWarning says that the installer which recorded these Skills
// still thinks it owns their directories.
func printInstallerWarning(out io.Writer, names []string, skillsDir string) {
	if len(names) == 0 {
		return
	}
	fmt.Fprintf(out, "%sAn installer lock file also records %s; that installer may still update %s in %s.%s\n",
		colorYellow, strings.Join(names, ", "), objectPronoun(len(names)), models.ToTildePath(skillsDir), colorReset)
}

func promptAdoptPlan(plan engine.AdoptPlan) ([]string, error) {
	options := make([]tui.SelectOption, 0, len(plan.Items))
	for _, item := range plan.Items {
		options = append(options, tui.SelectOption{Key: item.Name, Title: item.Name, Extra: adoptAction(item)})
	}
	return tui.PromptMultiSelect("Select skills to adopt:", options)
}

// reportAdoptOutcome words each Skill, then sums up in ADR-0002's codes: 0
// when every Skill was adopted, 1 when one was left for the user, 2 when one
// failed.
func reportAdoptOutcome(out io.Writer, result engine.AdoptResult, scope Scope) error {
	var recorded []string
	for _, skill := range result.Skills {
		switch {
		case skill.Outcome == engine.SyncDone && skill.Action == engine.AdoptDeclareRemote:
			fmt.Fprintf(out, "  %sAdopted %s from %s.%s\n", colorGreen, skill.Name, adoptSource(skill.Record.Source, skill.Subpath, ""), colorReset)
		case skill.Outcome == engine.SyncDone:
			fmt.Fprintf(out, "  %sAdopted %s: moved to %s.%s\n", colorGreen, skill.Name, models.ToTildePath(skill.MoveTo), colorReset)
		case skill.Declared && skill.Action == engine.AdoptDeclareRemote && skill.Reason != "":
			fmt.Fprintf(out, "  %sDeclared %s from %s without a Baseline: %s.%s\n", colorYellow, skill.Name, adoptSource(skill.Record.Source, skill.Subpath, ""), skill.Reason, colorReset)
		case skill.Outcome == engine.SyncBlocked && !skill.Declared:
			fmt.Fprintf(out, "  %sSkipped %s: %s%s\n", colorYellow, skill.Name, skill.Reason, colorReset)
		case skill.Reason != "":
			fmt.Fprintf(out, "  %sFailed to adopt %s: %s%s\n", colorRed, skill.Name, skill.Reason, colorReset)
		}
		if skill.Recorded && skill.Declared {
			recorded = append(recorded, skill.Name)
		}
	}
	for _, ev := range result.Events {
		if !syncEventIsProgress(ev.Kind) {
			printSyncEvent(out, ev)
		}
	}
	if result.StateError != "" {
		printScopeStateUnreadable(out, result.StateError)
	}
	printInstallerWarning(out, recorded, scope.SkillsDir)

	adopted := len(result.Adopted())
	configName := filepath.Base(scope.ConfigPath)
	if result.Blocked == 0 && result.Failed == 0 {
		fmt.Fprintf(out, "%sAdopted %s and updated %s.%s\n", colorGreen, countOf(adopted, "skill"), configName, colorReset)
		return nil
	}
	var parts []string
	if result.Blocked > 0 {
		parts = append(parts, fmt.Sprintf("%d blocked", result.Blocked))
	}
	if result.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", result.Failed))
	}
	fmt.Fprintf(out, "%sAdopted %d of %s; %s.%s\n", colorYellow, adopted, countOf(len(result.Skills), "skill"), strings.Join(parts, ", "), colorReset)
	if result.Failed > 0 {
		return exitError{message: fmt.Sprintf("Adopt did not complete: %s, %s", countOf(result.Failed, "failure"), countOf(result.Blocked, "blocked skill")), code: 2}
	}
	fmt.Fprintf(out, "Next: follow the reason given for each skill above, then run 'skills sync%s'.\n", scopeFlagOf(scope))
	return exitError{message: "some skills were not adopted", code: 1}
}
