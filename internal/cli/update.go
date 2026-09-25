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

			// Progress is transient and goes to stderr. Each refreshed Source,
			// error and rename is permanent and goes to stdout, above the
			// progress region.
			var region *presentation.Region
			errOut := cmd.ErrOrStderr()
			onProgress := func(ev engine.UpdateEvent) {
				if flagJSON {
					return
				}
				switch ev.Kind {
				case engine.UpdateCheckStart:
					region = presentation.StartRegion(errOut, "Checking "+countOf(ev.Total, "Source"), 0)
				case engine.UpdateCheckDone:
					region.Stop()
					region = nil
					if ev.Outdated == 0 {
						fmt.Fprintf(cmd.OutOrStdout(), "  %sAll %d Source Caches are already up to date.%s\n", colorGreen, ev.UpToDate, colorReset)
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "  %s%d Source Cache update(s) needed, %d already up to date.%s\n", colorCyan, ev.Outdated, ev.UpToDate, colorReset)
					}
				case engine.UpdateRefreshStart:
					region = presentation.StartRegion(errOut, "Refreshing "+countOf(ev.Total, "Source"), ev.Total)
				case engine.UpdateRefreshDone:
					region.Stop()
					region = nil
				case engine.UpdateStart:
					if ev.DryRun {
						fmt.Fprintf(cmd.OutOrStdout(), "  [%d/%d] %s[Dry-run]%s Would refresh %s%s%s\n", ev.Index, ev.Total, colorCyan, colorReset, colorBold, ev.Source, colorReset)
					} else {
						region.Start(presentation.Job{Name: ev.Source, Phase: "fetching"})
					}
				case engine.UpdateRepoDone:
					shaStr := ""
					if len(ev.NewSHA) >= 7 {
						shaStr = fmt.Sprintf(" (%s)", ev.NewSHA[:7])
					}
					region.DoneWith(ev.Source, func() {
						fmt.Fprintf(cmd.OutOrStdout(), "      %sUpdated %s%s%s%s.%s\n", colorGreen, colorBold, ev.Source, colorReset, shaStr, colorReset)
					})
				case engine.UpdateRenamed:
					region.Above(func() {
						fmt.Fprintf(cmd.OutOrStdout(), "      %s%s was renamed to %s%s%s in %s.%s\n", colorCyan, ev.From, colorBold, ev.To, colorReset+colorCyan, ev.Source, colorReset)
					})
				case engine.UpdateRepoError:
					region.Fail(ev.Source)
					region.Above(func() {
						fmt.Fprintf(cmd.OutOrStdout(), "      %sError updating %s: %s%s\n", colorRed, ev.Source, ev.Err, colorReset)
					})
				}
			}

			result, err := engine.UpdateRemoteSkills(cfg, targets, flagForce, flagDryRun, cacheDir, onProgress)
			region.Stop()
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

			if len(result.Renamed) > 0 && totalUpdated == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "%s%sFound %d renamed skill(s).%s\nRun 'skills sync' to apply the rename to this Scope.\n", colorBold, colorGreen, len(result.Renamed), colorReset)
			} else if totalUpdated > 0 {
				if flagDryRun {
					fmt.Fprintf(cmd.OutOrStdout(), "%s%sDry run complete: %d Source Cache(s) would be refreshed.%s%s\n", colorBold, colorGreen, totalUpdated, colorReset, skipMsg)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "%s%sRefreshed %d Source Cache(s).%s%s\nRun 'skills sync' to apply cached content to this Scope.\n", colorBold, colorGreen, totalUpdated, colorReset, skipMsg)
				}
			} else {
				if len(result.Errors) == 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "%s%sEverything is already up to date.%s\n", colorBold, colorGreen, colorReset)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "%s%sUpdate completed with errors.%s\n", colorBold, colorYellow, colorReset)
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
