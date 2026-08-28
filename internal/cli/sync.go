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
		Args:    cobra.NoArgs,
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
			fmt.Fprintf(out, "\n%s%sSyncing skills from %s...%s\n\n", colorBold, colorCyan, models.ToTildePath(configPath), colorReset)

			plan, err := engine.PlanSync(cfg, skillsDir, cacheDir)
			if err != nil {
				return err
			}
			decision := engine.SyncDecision{Force: flagForce}

			if flagDryRun {
				printSyncPlan(out, plan, decision)
				if !plan.Fresh(decision) {
					return fmt.Errorf("Sync did not converge: %d failure(s)", plan.Unresolved(decision))
				}
				fmt.Fprintf(out, "\n%s%sSkills sync complete. %d skills configured.%s\n\n", colorBold, colorGreen, len(plan.Names()), colorReset)
				return nil
			}

			if !flagForce && syncIsTerminal() {
				if unknown := plan.Unknown(); len(unknown) > 0 {
					allowUnknown, promptErr := syncPromptUnknown(out, unknown)
					if promptErr != nil {
						return promptErr
					}
					if !allowUnknown {
						return fmt.Errorf("Sync cancelled before making changes")
					}
					decision.AllowUnknown = true
				}
			}

			var progress *presentation.Progress
			report, err := plan.Apply(decision, func(ev engine.SyncEvent) {
				switch ev.Kind {
				case engine.SyncRefreshStart:
					progress = presentation.StartProgress(cmd.ErrOrStderr(), "Fetching Source: "+ev.Source+"...")
				case engine.SyncRefreshDone:
					progress.Stop()
					progress = nil
				}
			})
			progress.Stop()
			printSyncEvents(out, report)
			if err != nil {
				return err
			}

			fmt.Fprintf(out, "\n%s%sSkills sync complete. %d skills configured.%s\n\n", colorBold, colorGreen, len(report.Configured), colorReset)
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagForce, "force", false, "Overwrite local drift and unknown baselines from the existing Cache")
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Preview actions without making changes")

	return cmd
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
		fmt.Fprintf(out, "  %sSkipped %s: %s%s\n", colorYellow, item.Name, block, colorReset)
		return
	case action == engine.SyncActionMaterialize:
		fmt.Fprintf(out, "  [Dry-run] Would sync %s from %s\n", item.Name, item.Source)
	case action == engine.SyncActionSymlink && !item.LinkCurrent:
		fmt.Fprintf(out, "  [Dry-run] Would symlink %s -> %s\n", models.ToTildePath(item.LinkPath), models.ToTildePath(item.SourcePath))
	case action == engine.SyncActionCommand:
		fmt.Fprintf(out, "  [Dry-run] Would execute: %s\n", item.Command)
	}
	if len(item.Drift.Missing) > 0 {
		fmt.Fprintf(out, "  [Dry-run] Would link %s to %s.\n", item.Name, strings.Join(item.Drift.Missing, ", "))
	}
	if len(item.Drift.Unexpected) > 0 {
		fmt.Fprintf(out, "  [Dry-run] Would unlink %s from %s.\n", item.Name, strings.Join(item.Drift.Unexpected, ", "))
	}
}

func printSyncEvents(out io.Writer, report *engine.SyncReport) {
	if report == nil {
		return
	}
	for _, ev := range report.Events {
		switch ev.Kind {
		case engine.SyncRepoStart:
			fmt.Fprintf(out, "Syncing Source: %s%s%s (%d skills)...\n", colorBold, ev.Source, colorReset, len(ev.Skills))
		case engine.SyncWouldSync:
			fmt.Fprintf(out, "  [Dry-run] Would sync %s from %s\n", strings.Join(ev.Skills, ", "), ev.Source)
		case engine.SyncWouldDrift:
			if len(ev.Missing) > 0 {
				fmt.Fprintf(out, "  [Dry-run] Would link %s to %s.\n", ev.Skill, strings.Join(ev.Missing, ", "))
			}
			if len(ev.Unexpected) > 0 {
				fmt.Fprintf(out, "  [Dry-run] Would unlink %s from %s.\n", ev.Skill, strings.Join(ev.Unexpected, ", "))
			}
		case engine.SyncFetchFailed:
			fmt.Fprintf(out, "  %sFailed to fetch %s: %s%s\n", colorRed, ev.Source, ev.Err, colorReset)
		case engine.SyncPathMissing:
			fmt.Fprintf(out, "  %sSkill path missing in Source: %s for %s%s\n", colorRed, ev.Path, ev.Skill, colorReset)
		case engine.SyncCopyFailed:
			fmt.Fprintf(out, "  %sFailed to copy %s: %s%s\n", colorRed, ev.Skill, ev.Err, colorReset)
		case engine.SyncMaterialized:
			fmt.Fprintf(out, "  %sRestored %s%s%s.%s\n", colorGreen, colorBold, ev.Skill, colorReset, colorReset)
		case engine.SyncSourceMissing:
			fmt.Fprintf(out, "  %sWarning: Local symlink source missing: %s (skill: %s)%s\n", colorYellow, models.ToTildePath(ev.Path), ev.Skill, colorReset)
		case engine.SyncWouldSymlink:
			fmt.Fprintf(out, "  [Dry-run] Would symlink %s -> %s\n", models.ToTildePath(ev.Path), models.ToTildePath(ev.Target))
		case engine.SyncSymlinkFailed:
			fmt.Fprintf(out, "  %sFailed to symlink %s: %s%s\n", colorRed, ev.Skill, ev.Err, colorReset)
		case engine.SyncSymlinked:
			fmt.Fprintf(out, "  %sLinked local skill %s%s%s -> %s.%s\n", colorGreen, colorBold, ev.Skill, colorReset, models.ToTildePath(ev.Target), colorReset)
		case engine.SyncCheckFailed:
			fmt.Fprintf(out, "  %sCommand check '%s' failed, skipping %s%s\n", colorDim, ev.Path, ev.Skill, colorReset)
		case engine.SyncWouldCommand:
			fmt.Fprintf(out, "  [Dry-run] Would execute: %s\n", ev.Target)
		case engine.SyncCommandStart:
			fmt.Fprintf(out, "  Running installer for %s%s%s...\n", colorBold, ev.Skill, colorReset)
		case engine.SyncCommandFailed:
			fmt.Fprintf(out, "  %sFailed to run installer for %s: %s%s\n", colorRed, ev.Skill, ev.Err, colorReset)
		case engine.SyncSkipped:
			fmt.Fprintf(out, "  %sSkipped %s: %s%s\n", colorYellow, ev.Skill, ev.Err, colorReset)
		}
	}
}
