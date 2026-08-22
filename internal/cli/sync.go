package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	var (
		flagForce     bool
		flagPrune     bool
		flagPruneOnly bool
		flagDryRun    bool
		flagYes       bool
	)

	cmd := &cobra.Command{
		Use:     "sync",
		Aliases: []string{"restore"},
		Short:   "Sync and restore all skills declared in skills.json",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Past flag parsing, every failure below is a runtime problem rather
			// than misuse, so reporting it with a usage dump would mislead.
			cmd.SilenceUsage = true
			out := cmd.OutOrStdout()
			configPath, skillsDir, cacheDir := GetEffectivePaths()

			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}
			if flagPrune || flagPruneOnly {
				return runPrune(cmd, pruneOptions{yes: flagYes, dryRun: flagDryRun})
			}
			fmt.Fprintf(out, "\n%s%sSyncing skills from %s...%s\n\n", colorBold, colorCyan, models.ToTildePath(configPath), colorReset)

			if err := os.MkdirAll(skillsDir, 0755); err != nil {
				return err
			}

			configuredSkills := make(map[string]struct{})
			var locals []engine.LocalSyncSkill
			for name, localInfo := range cfg.Local {
				if localInfo.Type != "symlink" {
					continue
				}
				src := ResolveLocalSourcePath(localInfo.Source, skillsDir)
				locals = append(locals, engine.LocalSyncSkill{
					Name:       name,
					AbsSource:  src,
					LinkTarget: LocalSymlinkTarget(src, skillsDir),
				})
			}

			report, err := engine.SyncDeclared(cfg, skillsDir, cacheDir, locals, flagForce, flagDryRun)
			if err != nil {
				printSyncEvents(out, report)
				return err
			}
			printSyncEvents(out, report)
			for _, name := range report.Configured {
				configuredSkills[name] = struct{}{}
			}

			for name, localInfo := range cfg.Local {
				if localInfo.Type != "command" {
					continue
				}
				configuredSkills[name] = struct{}{}
				if localInfo.Check != "" {
					if _, _, err := engine.RunCmd(localInfo.Check, ""); err != nil {
						fmt.Fprintf(out, "  %sCommand check '%s' failed, skipping %s%s\n", colorDim, localInfo.Check, name, colorReset)
						continue
					}
				}
				if flagDryRun {
					fmt.Fprintf(out, "  [Dry-run] Would execute: %s\n", localInfo.Command)
					printDriftPreview(out, name, "local", cfg, skillsDir)
					continue
				}
				fmt.Fprintf(out, "  Running installer for %s%s%s...\n", colorBold, name, colorReset)
				_, _, _ = engine.RunCmd(localInfo.Command, "")
				if err := engine.ReconcileAgentSymlinks(name, "local", cfg, skillsDir); err != nil {
					return fmt.Errorf("failed to reconcile availability for %s: %w", name, err)
				}
			}

			// 3. Post-hooks
			if len(cfg.PostHooks) > 0 {
				fmt.Fprintf(out, "\n%sRunning post-sync hooks...%s\n", colorCyan, colorReset)
				hookResults := engine.ExecutePostHooks(cfg.PostHooks, flagDryRun)
				for _, h := range hookResults {
					badge := fmt.Sprintf("%sOK%s", colorGreen, colorReset)
					if !h.Success {
						badge = fmt.Sprintf("%sError%s", colorRed, colorReset)
					}
					fmt.Fprintf(out, "  %s [%s] %s\n", badge, h.Name, h.Message)
				}
			}

			fmt.Fprintf(out, "\n%s%sSkills sync complete. %d skills configured.%s\n\n", colorBold, colorGreen, len(configuredSkills), colorReset)
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagForce, "force", false, "Force re-clone and re-link all skills")
	cmd.Flags().BoolVar(&flagPrune, "prune", false, "Remove untracked skills and broken symlinks")
	cmd.Flags().BoolVar(&flagPruneOnly, "prune-only", false, "Remove untracked skills without restoring")
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip confirmation prompt when using deprecated prune flags")
	_ = cmd.Flags().MarkDeprecated("prune", "use `skills prune` instead")
	_ = cmd.Flags().MarkDeprecated("prune-only", "use `skills prune` instead")
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Preview actions without making changes")

	return cmd
}

func printDriftPreview(out io.Writer, name, source string, cfg *config.Config, skillsDir string) {
	missing, unexpected := engine.AgentLinkDrift(name, source, cfg, skillsDir)
	if len(missing) > 0 {
		fmt.Fprintf(out, "  [Dry-run] Would link %s to %s.\n", name, strings.Join(missing, ", "))
	}
	if len(unexpected) > 0 {
		fmt.Fprintf(out, "  [Dry-run] Would unlink %s from %s.\n", name, strings.Join(unexpected, ", "))
	}
}

func printSyncEvents(out io.Writer, report *engine.SyncReport) {
	if report == nil {
		return
	}
	for _, ev := range report.Events {
		switch ev.Kind {
		case engine.SyncRepoStart:
			fmt.Fprintf(out, "Syncing repo: %s%s%s (%d skills)...\n", colorBold, ev.Source, colorReset, len(ev.Skills))
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
			fmt.Fprintf(out, "  %sSkill path missing in repo: %s for %s%s\n", colorRed, ev.Path, ev.Skill, colorReset)
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
		}
	}
}
