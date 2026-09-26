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
			scope := ResolveScope()
			configPath, cacheDir := scope.ConfigPath, scope.CacheDir

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

			result := &engine.UpdateResult{UpdatedRepos: []engine.UpdatedRepoInfo{}, SkippedRepos: []engine.SkippedRepoInfo{}, Renamed: []engine.RenamedSkillInfo{}, Errors: []engine.UpdateErrorInfo{}}
			if len(cfg.Remote) > 0 {
				result, err = refreshSources(cmd, out, cfg, args, flagForce, flagDryRun, flagJSON, cacheDir)
				if err != nil {
					return err
				}
			}

			plan, err := engine.PlanSync(cfg, configPath, scope.SkillsDir, cacheDir)
			if err != nil {
				return err
			}
			decision := engine.SyncDecision{}
			planned := plan.Summary(decision)
			refreshed := len(result.UpdatedRepos)
			var updateErr error
			if len(result.Errors) > 0 {
				updateErr = fmt.Errorf("update completed with %d error(s)", len(result.Errors))
			}

			// A Scope that already matches its Config is left alone, so a
			// shell startup that runs update prints one line and changes nothing.
			if planned.Converged() {
				// A converged plan needed no Baseline, so an unreadable Scope
				// state is at most a warning (ADR-0002).
				if plan.StateVerdict() == engine.StateWarn {
					printScopeStateWarning(out, plan.StateError, scopeFlagOf(scope))
				}
				summary := newUpdateSyncJSON(planned, nil)
				switch {
				case flagJSON:
					printUpdateJSON(cmd.OutOrStdout(), result, summary)
				case refreshed == 0 && updateErr == nil:
					fmt.Fprintf(out, "%s%sEverything is already up to date.%s\n", colorBold, colorGreen, colorReset)
				case refreshed > 0:
					printRefreshed(out, refreshed, len(result.SkippedRepos), flagDryRun)
				}
				if updateErr != nil {
					fmt.Fprintf(out, "%s%sUpdate completed with errors.%s\n", colorBold, colorYellow, colorReset)
					return updateErr
				}
				if flagDryRun && refreshed > 0 {
					fmt.Fprintf(out, "Next: run 'skills update'.\n")
					return exitError{message: "Scope does not match its Config", code: 1}
				}
				return nil
			}

			if refreshed > 0 {
				printRefreshed(out, refreshed, len(result.SkippedRepos), flagDryRun)
			}
			outcome := planned
			var report *engine.SyncReport
			if flagDryRun {
				printSyncPlan(out, plan, decision, scopeFlagOf(scope))
			} else {
				var applyErr error
				if flagJSON {
					// Without a terminal to ask, unknown baselines stay blocked,
					// as they would for a Sync run from a script.
					report, applyErr = plan.Apply(decision, nil)
				} else {
					report, applyErr = applySyncPlan(cmd, out, scope, plan, decision)
				}
				if applyErr != nil {
					return applyErr
				}
				outcome = report.Summary()
			}
			syncErr := reportSyncOutcome(out, outcome, flagDryRun, "skills update")
			if flagJSON {
				printUpdateJSON(cmd.OutOrStdout(), result, newUpdateSyncJSON(outcome, report))
			}
			if updateErr != nil {
				fmt.Fprintf(out, "%s%sUpdate completed with errors.%s\n", colorBold, colorYellow, colorReset)
				return updateErr
			}
			return syncErr
		},
	}

	cmd.Flags().BoolVar(&flagForce, "force", false, "Re-fetch the Cache even if the commit SHA is unchanged; local drift stays protected")
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Preview the refresh and the Sync without making changes")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output machine-readable JSON")

	return cmd
}

// refreshSources refreshes the declared remote Sources, or only targets, in
// the shared Cache. Progress is transient and goes to stderr. Each refreshed
// Source and error is permanent and goes to out, above the progress region. A
// rename is left to the Sync that follows, which reports what it did with it.
func refreshSources(cmd *cobra.Command, out io.Writer, cfg *config.Config, targets []string, force, dryRun, quiet bool, cacheDir string) (*engine.UpdateResult, error) {
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
				fmt.Fprintf(out, "  [%d/%d] %s[Dry-run]%s Would refresh %s%s%s\n", ev.Index, ev.Total, colorCyan, colorReset, colorBold, ev.Source, colorReset)
			} else {
				region.Start(presentation.Job{Name: ev.Source, Phase: "fetching"})
			}
		case engine.UpdateRepoDone:
			shaStr := ""
			if len(ev.NewSHA) >= 7 {
				shaStr = fmt.Sprintf(" (%s)", ev.NewSHA[:7])
			}
			region.DoneWith(ev.Source, func() {
				fmt.Fprintf(out, "      %sUpdated %s%s%s%s.%s\n", colorGreen, colorBold, ev.Source, colorReset, shaStr, colorReset)
			})
		case engine.UpdateRepoUnchanged:
			// Counted with the Sources already up to date, not listed.
			region.Done(ev.Source)
		case engine.UpdateRepoError:
			region.Fail(ev.Source)
			region.Above(func() {
				fmt.Fprintf(out, "      %sError updating %s: %s%s\n", colorRed, ev.Source, ev.Err, colorReset)
			})
		}
	}

	result, err := engine.UpdateRemoteSkills(cfg, targets, force, dryRun, cacheDir, onProgress)
	region.Stop()
	return result, err
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
