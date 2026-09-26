package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/akunzai/skills-manager/internal/presentation"
	"github.com/akunzai/skills-manager/internal/tui"
	"github.com/spf13/cobra"
)

var syncIsTerminal = tui.IsTerminal

var syncPromptUnknown = func(out io.Writer, skills []engine.SkillFreshness) (bool, error) {
	for {
		choice, err := tui.PromptSelect("Replace Project Skills without a local baseline?", []tui.SelectOption{
			{Key: "overwrite", Title: "Overwrite"},
			{Key: "details", Title: "Show details"},
			{Key: "cancel", Title: "Cancel"},
		}, 2)
		if err != nil {
			return false, err
		}
		switch choice {
		case "overwrite":
			return true, nil
		case "details":
			printUnknownDetails(out, skills)
		default:
			return false, nil
		}
	}
}

func newSyncCmd() *cobra.Command {
	var (
		flagForce  bool
		flagDryRun bool
	)

	cmd := &cobra.Command{
		Use:     "sync",
		Aliases: []string{"restore"},
		Short:   "Sync and restore all skills declared in skills.json",
		Long: `Reconcile the selected Scope from skills.json and the existing Cache.

Exits 0 when the Scope matches its Config, 1 when it does not — a blocked skill
or, with --dry-run, work still to do — and 2 when the work could not be
completed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Past flag parsing, every failure below is a runtime problem rather
			// than misuse, so reporting it with a usage dump would mislead.
			cmd.SilenceUsage = true
			out := cmd.OutOrStdout()
			scope := ResolveScope()
			configPath, skillsDir, cacheDir := scope.ConfigPath, scope.SkillsDir, scope.CacheDir

			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}

			plan, err := engine.PlanSync(cfg, configPath, skillsDir, cacheDir)
			if err != nil {
				return err
			}
			decision := engine.SyncDecision{Force: flagForce}

			if flagDryRun {
				printSyncPlan(out, plan, decision)
				return reportSyncOutcome(out, plan.Summary(decision), true, "skills sync")
			}

			report, err := applySyncPlan(cmd, out, scope, plan, decision)
			if err != nil {
				return err
			}
			return reportSyncOutcome(out, report.Summary(), false, "skills sync")
		},
	}

	cmd.Flags().BoolVar(&flagForce, "force", false, "Overwrite local drift and unknown baselines from the existing Cache")
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Preview actions without making changes")

	return cmd
}

// applySyncPlan asks about unknown baselines when a person is there to answer,
// then applies plan with progress on stderr. The report's Summary already
// answers whether --force would lift what is left under the decision applied.
// Sync and update both reconcile a Scope through it.
func applySyncPlan(cmd *cobra.Command, out io.Writer, scope Scope, plan *engine.SyncPlan, decision engine.SyncDecision) (*engine.SyncReport, error) {
	if !decision.Force && syncIsTerminal() {
		if unknown := plan.Unknown(); len(unknown) > 0 {
			allowUnknown, promptErr := syncPromptUnknown(out, unknown)
			if promptErr != nil {
				return nil, promptErr
			}
			// Declining leaves those Skills blocked, as a Sync without a
			// terminal would, and Sync still reconciles the rest. The Scope
			// then does not match its Config: exit 1, not a failure
			// (ADR-0002).
			decision.AllowUnknown = allowUnknown
		}
	}

	// With nothing declared there is no progress to show; a nil region
	// shows none.
	var region *presentation.Region
	if n := len(plan.Items); n > 0 {
		region = presentation.StartRegion(cmd.ErrOrStderr(), "Syncing "+countOf(n, "Skill"), n)
	}
	report, err := plan.Apply(decision, func(ev engine.SyncEvent) { showSyncProgress(region, out, ev) })
	region.Stop()
	// The flag the user passed, not the shape of --skills-dir (root.go).
	scopeFlag := ""
	if scope.IsProject {
		scopeFlag = " -p"
	}
	printCopiedAvailability(out, report, scopeFlag)
	return report, err
}

// reportSyncOutcome states where the Scope stands and picks the exit code.
// Sync speaks the same three codes as outdated: 0 converged, 1 not converged,
// 2 the work could not be completed. next is the command that finishes
// pending work. A blocked Skill is a state to decide on,
// not an error, so it never reads as a failure.
func reportSyncOutcome(out io.Writer, summary engine.SyncSummary, dryRun bool, next string) error {
	if summary.Failed > 0 {
		return exitError{message: fmt.Sprintf("Sync did not converge: %s, %s", countOf(summary.Failed, "failure"), countOf(summary.Blocked, "blocked skill")), code: 2}
	}
	if summary.Blocked > 0 || summary.Pending > 0 {
		parts := make([]string, 0, 2)
		if summary.Pending > 0 {
			parts = append(parts, countOf(summary.Pending, "skill")+" to reconcile")
		}
		if summary.Blocked > 0 {
			parts = append(parts, countOf(summary.Blocked, "blocked skill"))
		}
		fmt.Fprintf(out, "%s%sSync did not converge. %s.%s\n", colorBold, colorYellow, strings.Join(parts, ", "), colorReset)
		if summary.Blocked > 0 && summary.Forceable {
			fmt.Fprintf(out, "Next: inspect the changes, then re-run with 'skills sync --force' to overwrite them.\n")
		} else if summary.Blocked > 0 {
			fmt.Fprintf(out, "Next: follow the reason given for each skipped skill above.\n")
		} else {
			fmt.Fprintf(out, "Next: run '%s'.\n", next)
		}
		return exitError{message: "Scope does not match its Config", code: 1}
	}
	if dryRun {
		fmt.Fprintf(out, "%s%sScope already matches its Config. %d skills declared.%s\n", colorBold, colorGreen, summary.Configured, colorReset)
		return nil
	}
	fmt.Fprintf(out, "%s%sSkills sync complete. %d skills configured.%s\n", colorBold, colorGreen, summary.Configured, colorReset)
	return nil
}

func countOf(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func printUnknownDetails(out io.Writer, skills []engine.SkillFreshness) {
	for _, skill := range skills {
		fmt.Fprintf(out, "\n%s (%s)\n  Scope: %s\n  Cache: %s\n", skill.Name, skill.Source, models.ToTildePath(skill.ScopePath), models.ToTildePath(skill.CachePath))
		for _, group := range []struct {
			label string
			paths []string
		}{{"added", skill.Changes.Added}, {"removed", skill.Changes.Removed}, {"modified", skill.Changes.Modified}} {
			for _, path := range group.paths {
				fmt.Fprintf(out, "  %s: %s\n", group.label, path)
			}
		}
	}
}

// printSyncPlan renders what Sync would do, straight from the plan. Nothing
// here touches the filesystem or runs a Skill-supplied command.
func printSyncPlan(out io.Writer, plan *engine.SyncPlan, decision engine.SyncDecision) {
	if plan.StateError != "" {
		fmt.Fprintf(out, "  %sSkipped : %s%s\n", colorYellow, plan.StateError, colorReset)
	}
	for _, source := range plan.Sources {
		items := plan.SourceItems(source)
		fmt.Fprintf(out, "Syncing Source: %s%s%s (%d skills)...\n", colorBold, source, colorReset, len(items))
		for _, item := range items {
			printSyncPlanItem(out, item, decision)
		}
	}
	for _, item := range plan.LocalItems() {
		printSyncPlanItem(out, item, decision)
	}
}

func printSyncPlanItem(out io.Writer, item engine.SyncPlanItem, decision engine.SyncDecision) {
	if item.Err != "" {
		fmt.Fprintf(out, "  %sFailed to fetch %s: %s%s\n", colorRed, item.Source, item.Err, colorReset)
		return
	}
	action, block := item.Resolve(decision)
	switch {
	case block == engine.SyncBlockSourceMissing:
		fmt.Fprintf(out, "  %sWarning: Local symlink source missing: %s (skill: %s)%s\n", colorYellow, models.ToTildePath(item.SourcePath), item.Name, colorReset)
		return
	case action == engine.SyncActionSkip:
		reason := string(block)
		if item.BlockReason != "" {
			reason += ": " + item.BlockReason
		}
		fmt.Fprintf(out, "  %sSkipped %s: %s%s\n", colorYellow, item.Name, reason, colorReset)
		return
	case action == engine.SyncActionRename && item.RenameTargetDeclared:
		fmt.Fprintf(out, "  [Dry-run] Would remove %s, renamed upstream to %s, which is already declared\n", item.Name, item.Freshness.RenamedTo)
		return
	case action == engine.SyncActionRename:
		fmt.Fprintf(out, "  [Dry-run] Would rename %s to %s as its Source declares: sync %s and remove %s\n", item.Name, item.Freshness.RenamedTo, item.Freshness.RenamedTo, item.Name)
		return
	case action == engine.SyncActionMaterialize:
		fmt.Fprintf(out, "  [Dry-run] Would sync %s from %s\n", item.Name, item.Source)
	case action == engine.SyncActionSymlink && !item.LinkCurrent:
		fmt.Fprintf(out, "  [Dry-run] Would symlink %s -> %s\n", models.ToTildePath(item.LinkPath), models.ToTildePath(item.SourcePath))
	case action == engine.SyncActionCommand && !item.Installed:
		fmt.Fprintf(out, "  [Dry-run] Would execute: %s\n", item.Command)
	}
	if len(item.Drift.Missing) > 0 {
		fmt.Fprintf(out, "  [Dry-run] Would link %s to %s.\n", item.Name, strings.Join(item.Drift.Missing, ", "))
	}
	if len(item.Drift.Broken) > 0 {
		fmt.Fprintf(out, "  [Dry-run] Would repair broken availability for %s on %s.\n", item.Name, strings.Join(item.Drift.Broken, ", "))
	}
	if len(item.Drift.Unexpected) > 0 {
		fmt.Fprintf(out, "  [Dry-run] Would unlink %s from %s.\n", item.Name, strings.Join(item.Drift.Unexpected, ", "))
	}
}

// printCopiedAvailability says once, after the per-Skill results, that
// Availability was applied by copying and why. Per-Skill it would be noise;
// omitted entirely, a Windows user has no way to tell that their Agent
// directories hold real files on purpose. Left unstyled on purpose: a copy is
// a working Availability by another mechanism (ADR-0003), not a warning, and
// Sync counts it towards neither blocked nor failed.
func printCopiedAvailability(out io.Writer, report *engine.SyncReport, scopeFlag string) {
	if report == nil {
		return
	}
	copied := 0
	for _, ev := range report.Events {
		if ev.Kind == engine.SyncAvailabilityCopied {
			copied += len(ev.Agents)
		}
	}
	if copied == 0 {
		return
	}
	fmt.Fprintln(out, "\n"+copiedAvailabilityNotice(copied, "", scopeFlag))
}

// showSyncProgress moves one Skill's row through the region. Only what stands
// in the way, or a rename, stays on screen; a line that just says a step went
// well would repeat the row it replaces.
func showSyncProgress(region *presentation.Region, out io.Writer, ev engine.SyncEvent) {
	switch ev.Kind {
	case engine.SyncItemStart:
		region.Start(presentation.Job{Name: ev.Skill, Phase: syncPhase(ev.Action)})
	case engine.SyncMaterialized:
		region.SetPhase(ev.Skill, "linking")
	case engine.SyncCommandStart:
		region.SetPhase(ev.Skill, "running installer")
	case engine.SyncItemDone:
		if ev.Outcome == engine.SyncDone {
			region.Done(ev.Skill)
		} else {
			region.Fail(ev.Skill)
		}
	default:
		if !syncEventIsProgress(ev.Kind) {
			region.Above(func() { printSyncEvent(out, ev) })
		}
	}
}

func syncPhase(action engine.SyncAction) string {
	switch action {
	case engine.SyncActionMaterialize:
		return "materializing"
	case engine.SyncActionRename:
		return "renaming"
	case engine.SyncActionCommand:
		return "running installer"
	case engine.SyncActionSkip:
		return "checking"
	default:
		return "linking"
	}
}

// syncEventIsProgress is whether an event only says a step went well, which
// the progress region shows instead of a line: a Source or Skill starting,
// Materializing, linking, starting an installer, and the Availability-copied
// notice, which is summed up once afterwards.
func syncEventIsProgress(kind string) bool {
	switch kind {
	case engine.SyncRepoStart, engine.SyncMaterialized, engine.SyncSymlinked, engine.SyncCommandStart, engine.SyncAvailabilityCopied, engine.SyncItemStart, engine.SyncItemDone:
		return true
	default:
		return false
	}
}

// printSyncEvent words one Sync event that stays on screen: what stands in a
// Skill's way, or a rename. Add prints the events that say why a Skill was not
// applied through it too, so that reason reads the same whichever command
// applied the Skill. Events that only say a step went well have no words here;
// the progress region shows them.
func printSyncEvent(out io.Writer, ev engine.SyncEvent) {
	switch ev.Kind {
	case engine.SyncFetchFailed:
		fmt.Fprintf(out, "  %sFailed to fetch %s: %s%s\n", colorRed, ev.Source, ev.Err, colorReset)
	case engine.SyncPathMissing:
		fmt.Fprintf(out, "  %sSkill path missing in Source: %s for %s%s\n", colorRed, ev.Path, ev.Skill, colorReset)
	case engine.SyncAvailabilityFailed:
		fmt.Fprintf(out, "  %sFailed to apply availability for %s: %s%s\n", colorRed, ev.Skill, ev.Err, colorReset)
	case engine.SyncCopyFailed:
		fmt.Fprintf(out, "  %sFailed to copy %s: %s%s\n", colorRed, ev.Skill, ev.Err, colorReset)
	case engine.SyncSourceMissing:
		fmt.Fprintf(out, "  %sWarning: Local symlink source missing: %s (skill: %s)%s\n", colorYellow, models.ToTildePath(ev.Path), ev.Skill, colorReset)
	case engine.SyncSymlinkFailed:
		fmt.Fprintf(out, "  %sFailed to symlink %s: %s%s\n", colorRed, ev.Skill, ev.Err, colorReset)
	case engine.SyncCheckFailed:
		fmt.Fprintf(out, "  %sCommand check '%s' failed, skipping %s%s\n", colorDim, ev.Path, ev.Skill, colorReset)
	case engine.SyncCommandFailed:
		fmt.Fprintf(out, "  %sFailed to run installer for %s: %s%s\n", colorRed, ev.Skill, ev.Err, colorReset)
	case engine.SyncRenamed:
		fmt.Fprintf(out, "  %sRenamed %s%s%s to %s%s%s, as its Source declares.%s\n", colorGreen, colorBold, ev.Skill, colorReset+colorGreen, colorBold, ev.Target, colorReset+colorGreen, colorReset)
	case engine.SyncRenameFailed:
		fmt.Fprintf(out, "  %sFailed to rename %s to %s: %s%s\n", colorRed, ev.Skill, ev.Target, ev.Err, colorReset)
	case engine.SyncSkipped:
		fmt.Fprintf(out, "  %sSkipped %s: %s%s\n", colorYellow, ev.Skill, ev.Err, colorReset)
	case engine.SyncStateFailed:
		if ev.Skill == "" {
			printScopeStateUnreadable(out, ev.Err)
			break
		}
		fmt.Fprintf(out, "  %sFailed to record the baseline for %s: %s%s\n", colorRed, ev.Skill, ev.Err, colorReset)
	}
}

// printScopeStateUnreadable is the one sentence every command prints when the
// Scope state cannot be read and Baselines go unrecorded.
func printScopeStateUnreadable(out io.Writer, reason string) {
	fmt.Fprintf(out, "  %sFailed to read the Scope baseline: %s%s\n", colorRed, reason, colorReset)
}
