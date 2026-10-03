package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/presentation"
	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	var (
		flagForce  bool
		flagDryRun bool
		flagJSON   bool
	)

	cmd := &cobra.Command{
		Use:     "update [targets...]",
		Aliases: []string{"upgrade"},
		Short:   "Refresh remote Sources and sync the Scope",
		Long: `Refresh remote Sources in the shared Cache, then reconcile the selected Scope
from skills.json, as 'skills sync' would. Sync covers the whole Scope, even when
targets narrow the refresh.

Exits 0 when the Scope matches its Config, 1 when it does not — a blocked skill
or, with --dry-run, work still to do — and 2 when the work could not be
completed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Past flag parsing, every failure below is a runtime problem rather
			// than misuse, so reporting it with a usage dump would mislead.
			cmd.SilenceUsage = true
			scope := ResolveScope(cmd)
			configPath := scope.ConfigPath

			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}

			// JSON keeps stdout for the document alone: the text below goes
			// nowhere, and no prompt can wait for an answer.
			out := cmd.OutOrStdout()
			if flagJSON {
				out = io.Discard
			}

			prepared, err := prepareUpdate(cmd, out, cfg, scope, engine.UpdateOptions{Targets: args, Force: flagForce, DryRun: flagDryRun}, flagJSON)
			if err != nil {
				return err
			}
			decision := engine.SyncDecision{}
			var progress func(engine.SyncEvent)
			stop := func() {}
			// Presentation follows the prepared path; execution stays in engine.
			if prepared.Path != engine.UpdateConverged && len(prepared.Refresh.UpdatedRepos) > 0 {
				printRefreshed(out, len(prepared.Refresh.UpdatedRepos), len(prepared.Refresh.SkippedRepos), flagDryRun)
			}
			switch prepared.Path {
			case engine.UpdatePreview:
				printSyncPlan(out, prepared.Sync, decision, scopeFlagsOf(cmd, scope))
			case engine.UpdateApply:
				if !flagJSON {
					decision, err = decideSync(cmd, out, prepared.Sync, decision)
					if err != nil {
						return err
					}
					progress, stop = syncProgress(cmd, out, scope, prepared.Sync)
				}
			}
			outcome, err := prepared.Finish(decision, progress)
			stop()
			if err != nil {
				return err
			}
			if outcome.Report != nil && !flagJSON {
				printSyncReport(out, scopeFlagsOf(cmd, scope), outcome.Report)
			}
			return reportUpdateOutcome(cmd, out, scope, prepared, outcome, flagDryRun, flagJSON)
		},
	}

	cmd.Flags().BoolVar(&flagForce, "force", false, "Re-fetch the Cache even if the commit SHA is unchanged; local drift stays protected")
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Preview the refresh and the Sync without making changes")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output machine-readable JSON")

	return cmd
}

// prepareUpdate refreshes the declared remote Sources, or only targets, in
// the shared Cache. Progress is transient and goes to stderr. Each refreshed
// Source and error is permanent and goes to out, above the progress region. A
// rename is left to the Sync that follows, which reports what it did with it.
func prepareUpdate(cmd *cobra.Command, out io.Writer, cfg *config.Config, scope Scope, options engine.UpdateOptions, quiet bool) (*engine.PreparedUpdate, error) {
	progress, stop := updateProgress(cmd, out, quiet)
	defer stop()
	return engine.PrepareUpdate(cfg, scope.ConfigPath, scope.SkillsDir, scope.CacheDir, options, progress)
}

// refreshSources is the refresh-only adapter used by diff --fetch.
func refreshSources(cmd *cobra.Command, out io.Writer, cfg *config.Config, targets []string, force, dryRun, quiet bool, cacheDir string) (*engine.UpdateResult, error) {
	progress, stop := updateProgress(cmd, out, quiet)
	defer stop()
	return engine.UpdateRemoteSkills(cfg, targets, force, dryRun, cacheDir, progress)
}

func updateProgress(cmd *cobra.Command, out io.Writer, quiet bool) (engine.UpdateProgress, func()) {
	var region *presentation.Region
	errOut := cmd.ErrOrStderr()
	onProgress := func(ev engine.UpdateEvent) {
		if quiet {
			return
		}
		switch ev.Kind {
		case engine.UpdateCheckStart:
			region = presentation.StartRegion(errOut, "Checking "+countOf(ev.Total, "Source"), 0)
		case engine.UpdateCheckDone:
			region.Stop()
			// No count here: a Source whose new commit leaves its Skills
			// unchanged is only known after the refresh, and the summary
			// line counts it then.
			region = nil
		case engine.UpdateRefreshStart:
			region = presentation.StartRegion(errOut, "Refreshing "+countOf(ev.Total, "Source"), ev.Total)
		case engine.UpdateRefreshDone:
			region.Stop()
			region = nil
		case engine.UpdateStart:
			if ev.DryRun {
				fmt.Fprintf(out, "[%d/%d] %s[Dry-run]%s Would refresh %s%s%s\n", ev.Index, ev.Total, colorCyan, colorReset, colorBold, ev.Source, colorReset)
			} else {
				region.Start(presentation.Job{Name: ev.Source, Phase: "fetching"})
			}
		case engine.UpdateRepoDone:
			shaStr := ""
			if len(ev.NewSHA) >= 7 {
				shaStr = fmt.Sprintf(" (%s)", ev.NewSHA[:7])
			}
			region.DoneWith(ev.Source, func() {
				fmt.Fprintf(out, "%sUpdated %s%s%s%s.%s\n", colorGreen, colorBold, ev.Source, colorReset, shaStr, colorReset)
			})
		case engine.UpdateRepoUnchanged:
			// Counted with the Sources already up to date, not listed.
			region.Done(ev.Source)
		case engine.UpdateTrustRootError:
			// After the refresh, so no region is drawing.
			fmt.Fprintf(out, "%sError: %s%s\n", colorRed, ev.Err, colorReset)
		case engine.UpdateRepoError:
			region.Fail(ev.Source)
			region.Above(func() {
				fmt.Fprintf(out, "%sError updating %s: %s%s\n", colorRed, ev.Source, ev.Err, colorReset)
			})
		}
	}

	return onProgress, func() { region.Stop() }
}

// reportUpdateOutcome preserves Update's wording and maps the engine's final
// disposition to the existing exit codes. Sync is still rendered when a
// refresh failure determines the final state.
func reportUpdateOutcome(cmd *cobra.Command, out io.Writer, scope Scope, prepared *engine.PreparedUpdate, outcome *engine.UpdateOutcome, dryRun, jsonOutput bool) error {
	result := outcome.Refresh
	refreshed := len(result.UpdatedRepos)
	var syncErr error
	if prepared.Path == engine.UpdateConverged {
		if prepared.Sync.StateVerdict() == engine.StateWarn {
			printScopeStateWarning(out, prepared.Sync.StateError, scopeFlagsOf(cmd, scope))
		}
		switch {
		case jsonOutput:
			printUpdateJSON(cmd.OutOrStdout(), result, newUpdateSyncJSON(outcome.Summary, outcome.Report))
		case refreshed == 0 && outcome.State != engine.UpdateRefreshFailed:
			fmt.Fprintf(out, "%s%sEverything is already up to date.%s\n", colorBold, colorGreen, colorReset)
		case refreshed > 0:
			printRefreshed(out, refreshed, len(result.SkippedRepos), dryRun)
		}
	} else {
		syncErr = reportSyncOutcome(out, outcome.Summary, dryRun, "update", scopeFlagsOf(cmd, scope))
		if jsonOutput {
			printUpdateJSON(cmd.OutOrStdout(), result, newUpdateSyncJSON(outcome.Summary, outcome.Report))
		}
	}
	switch outcome.State {
	case engine.UpdateRefreshFailed:
		fmt.Fprintf(out, "%s%sUpdate completed with errors.%s\n", colorBold, colorYellow, colorReset)
		return fmt.Errorf("update completed with %d error(s)", len(result.Errors))
	case engine.UpdatePendingRefresh:
		fmt.Fprintf(out, "Next: run 'skills update%s'.\n", scopeFlagsOf(cmd, scope))
		return exitError{message: "Scope does not match its Config", code: 1}
	case engine.UpdatePendingSync, engine.UpdateSyncFailed:
		return syncErr
	default:
		return nil
	}
}

func printRefreshed(out io.Writer, refreshed, skipped int, dryRun bool) {
	skipMsg := ""
	if skipped > 0 {
		skipMsg = fmt.Sprintf(" (%d Source Cache(s) were already up to date)", skipped)
	}
	if dryRun {
		fmt.Fprintf(out, "%s%sDry run complete: %d Source Cache(s) would be refreshed.%s%s\n", colorBold, colorGreen, refreshed, colorReset, skipMsg)
		return
	}
	fmt.Fprintf(out, "%s%sRefreshed %d Source Cache(s).%s%s\n", colorBold, colorGreen, refreshed, colorReset, skipMsg)
}

// updateSyncJSON is where the Scope stands after update's Sync. Pending is
// only ever non-zero under --dry-run; Updated and Restored are only ever
// non-empty without it.
type updateSyncJSON struct {
	Converged  bool     `json:"converged"`
	Configured int      `json:"configured"`
	Pending    int      `json:"pending"`
	Blocked    int      `json:"blocked"`
	Failed     int      `json:"failed"`
	Updated    []string `json:"updated"`
	Restored   []string `json:"restored"`
}

// newUpdateSyncJSON words summary, and the Skills report wrote when Sync ran.
func newUpdateSyncJSON(summary engine.SyncSummary, report *engine.SyncReport) updateSyncJSON {
	doc := updateSyncJSON{
		Converged:  summary.Converged(),
		Configured: summary.Configured,
		Pending:    summary.Pending,
		Blocked:    summary.Blocked,
		Failed:     summary.Failed,
		Updated:    []string{},
		Restored:   []string{},
	}
	if report != nil {
		doc.Updated = append(doc.Updated, report.Updated...)
		doc.Restored = append(doc.Restored, report.Restored...)
	}
	return doc
}

// printUpdateJSON keeps the Update document's shape and adds the Sync that
// followed it.
func printUpdateJSON(out io.Writer, result *engine.UpdateResult, summary updateSyncJSON) {
	data, _ := json.MarshalIndent(struct {
		*engine.UpdateResult
		Sync updateSyncJSON `json:"sync"`
	}{result, summary}, "", "  ")
	fmt.Fprintln(out, string(data))
}
