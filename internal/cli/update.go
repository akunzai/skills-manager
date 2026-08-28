package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/presentation"
	"github.com/spf13/cobra"
)

var startUpdateProgress = presentation.StartProgress

func newUpdateCmd() *cobra.Command {
	var (
		flagForce  bool
		flagDryRun bool
		flagJSON   bool
	)

	cmd := &cobra.Command{
		Use:     "update [targets...]",
		Aliases: []string{"upgrade"},
		Short:   "Refresh remote Sources in the shared Cache",
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

			if len(cfg.Remote) == 0 {
				if flagJSON {
					fmt.Fprintln(cmd.OutOrStdout(), `{"updated_repos":[],"skipped_repos":[],"errors":[]}`)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "%sNo remote Sources configured in %s.%s\n", colorYellow, filepath.Base(configPath), colorReset)
				}
				return nil
			}

			targets := args

			if !flagJSON {
				if flagDryRun {
					fmt.Fprintf(cmd.OutOrStdout(), "\n%s%s[Dry-run] Checking Cache updates...%s\n\n", colorBold, colorCyan, colorReset)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "\n%s%sRefreshing remote Sources in the shared Cache...%s\n\n", colorBold, colorCyan, colorReset)
				}
			}

			var progress *presentation.Progress
			onProgress := func(ev engine.UpdateEvent) {
				if flagJSON {
					return
				}
				switch ev.Kind {
				case engine.UpdateCheckStart:
					progress = startUpdateProgress(cmd.ErrOrStderr(), fmt.Sprintf("Checking %d remote Sources in parallel...", ev.Total))
				case engine.UpdateCheckDone:
					progress.Stop()
					progress = nil
					if ev.Outdated == 0 {
						fmt.Fprintf(cmd.OutOrStdout(), "  %sAll %d Source Caches are already up to date.%s\n\n", colorGreen, ev.UpToDate, colorReset)
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "  %s%d Source Cache update(s) needed, %d already up to date.%s\n\n", colorCyan, ev.Outdated, ev.UpToDate, colorReset)
					}
				case engine.UpdateRefreshDone:
					progress.Stop()
					progress = nil
				case engine.UpdateStart:
					if ev.DryRun {
						fmt.Fprintf(cmd.OutOrStdout(), "  [%d/%d] %s[Dry-run]%s Would refresh %s%s%s\n", ev.Index, ev.Total, colorCyan, colorReset, colorBold, ev.Source, colorReset)
					} else {
						progress = startUpdateProgress(cmd.ErrOrStderr(), fmt.Sprintf("[%d/%d] Refreshing %s...", ev.Index, ev.Total, ev.Source))
					}
				case engine.UpdateRepoDone:
					progress.Stop()
					progress = nil
					shaStr := ""
					if len(ev.NewSHA) >= 7 {
						shaStr = fmt.Sprintf(" (%s)", ev.NewSHA[:7])
					}
					fmt.Fprintf(cmd.OutOrStdout(), "      %sUpdated %s%s%s%s.%s\n", colorGreen, colorBold, ev.Source, colorReset, shaStr, colorReset)
				case engine.UpdateRepoError:
					progress.Stop()
					progress = nil
					fmt.Fprintf(cmd.OutOrStdout(), "      %sError updating %s: %s%s\n", colorRed, ev.Source, ev.Err, colorReset)
				}
			}

			result, err := engine.UpdateRemoteSkills(cfg, targets, flagForce, flagDryRun, cacheDir, onProgress)
			progress.Stop()
			if err != nil {
				return err
			}

			if flagJSON {
				data, _ := json.MarshalIndent(result, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				if len(result.Errors) > 0 {
					return fmt.Errorf("update completed with errors")
				}
				return nil
			}

			totalUpdated := len(result.UpdatedRepos)
			totalSkipped := len(result.SkippedRepos)

			skipMsg := ""
			if totalSkipped > 0 {
				skipMsg = fmt.Sprintf(" (%d Source Cache(s) were already up to date)", totalSkipped)
			}

			if totalUpdated > 0 {
				if flagDryRun {
					fmt.Fprintf(cmd.OutOrStdout(), "\n%s%sDry run complete: %d Source Cache(s) would be refreshed.%s%s\n\n", colorBold, colorGreen, totalUpdated, colorReset, skipMsg)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "\n%s%sRefreshed %d Source Cache(s).%s%s\nRun 'skills sync' to apply cached content to this Scope.\n\n", colorBold, colorGreen, totalUpdated, colorReset, skipMsg)
				}
			} else {
				if len(result.Errors) == 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "\n%s%sEverything is already up to date.%s\n\n", colorBold, colorGreen, colorReset)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "\n%s%sUpdate completed with errors.%s\n\n", colorBold, colorYellow, colorReset)
				}
			}

			if len(result.Errors) > 0 {
				return fmt.Errorf("update completed with %d error(s)", len(result.Errors))
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagForce, "force", false, "Force re-fetch and overwrite even if commit SHA is unchanged")
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Preview updates without making changes")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output machine-readable JSON")

	return cmd
}
