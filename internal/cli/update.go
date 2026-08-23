package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

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
		Short:   "Fetch updates and refresh remote skills in the selected scope",
		Long: `Fetch remote repository updates and refresh installed remote skills in the
selected global or project scope. The Git cache is shared between scopes, but
skills are written to the selected scope's skills directory.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Past flag parsing, every failure below is a runtime problem rather
			// than misuse, so reporting it with a usage dump would mislead.
			cmd.SilenceUsage = true
			scope := ResolveScope()
			configPath, skillsDir, cacheDir := scope.ConfigPath, scope.SkillsDir, scope.CacheDir

			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}

			if len(cfg.Remote) == 0 {
				if flagJSON {
					fmt.Fprintln(cmd.OutOrStdout(), `{"updated_repos":[],"updated_skills":[],"skipped_repos":[],"errors":[]}`)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "%sNo remote repositories configured in %s.%s\n", colorYellow, filepath.Base(configPath), colorReset)
				}
				return nil
			}

			targets := args

			if !flagJSON {
				if flagDryRun {
					fmt.Fprintf(cmd.OutOrStdout(), "\n%s%s[Dry-run] Checking and previewing skills update...%s\n\n", colorBold, colorCyan, colorReset)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "\n%s%sUpdating skills from remote repositories...%s\n\n", colorBold, colorCyan, colorReset)
				}
			}

			var progress *presentation.Progress
			onProgress := func(ev engine.UpdateEvent) {
				if flagJSON {
					return
				}
				switch ev.Kind {
				case engine.UpdateCheckStart:
					fmt.Fprintf(cmd.OutOrStdout(), "  Checking %d remote repositories in parallel...\n", ev.Total)
				case engine.UpdateCheckDone:
					if ev.Outdated == 0 {
						fmt.Fprintf(cmd.OutOrStdout(), "  %sAll %d repositories are already up to date.%s\n\n", colorGreen, ev.UpToDate, colorReset)
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "  %s%d repository update(s) needed, %d already up to date.%s\n\n", colorCyan, ev.Outdated, ev.UpToDate, colorReset)
					}
				case engine.UpdateRefreshStart:
					progress = presentation.StartProgress(cmd.ErrOrStderr(), fmt.Sprintf("Refreshing remote Sources (%d)...", ev.Total))
				case engine.UpdateRefreshDone:
					progress.Stop()
					progress = nil
				case engine.UpdateStart:
					skillsList := strings.Join(ev.Skills, ", ")
					if ev.DryRun {
						fmt.Fprintf(cmd.OutOrStdout(), "  [%d/%d] %s[Dry-run]%s Would update %s%s%s (%s)\n", ev.Index, ev.Total, colorCyan, colorReset, colorBold, ev.Source, colorReset, skillsList)
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "  [%d/%d] Updating %s%s%s (%s)...\n", ev.Index, ev.Total, colorBold, ev.Source, colorReset, skillsList)
					}
				case engine.UpdateSkillRestored:
					fmt.Fprintf(cmd.OutOrStdout(), "      Restored %s%s%s\n", colorBold, ev.Skill, colorReset)
				case engine.UpdateRepoDone:
					shaStr := ""
					if len(ev.NewSHA) >= 7 {
						shaStr = fmt.Sprintf(" (%s)", ev.NewSHA[:7])
					}
					fmt.Fprintf(cmd.OutOrStdout(), "      %sUpdated %s%s%s%s.%s\n", colorGreen, colorBold, ev.Source, colorReset, shaStr, colorReset)
				case engine.UpdateRepoError:
					fmt.Fprintf(cmd.OutOrStdout(), "      %sError updating %s: %s%s\n", colorRed, ev.Source, ev.Err, colorReset)
				case engine.UpdateWouldDrift:
					if len(ev.Missing) > 0 {
						fmt.Fprintf(cmd.OutOrStdout(), "      [Dry-run] Would link %s to %s.\n", ev.Skill, strings.Join(ev.Missing, ", "))
					}
					if len(ev.Unexpected) > 0 {
						fmt.Fprintf(cmd.OutOrStdout(), "      [Dry-run] Would unlink %s from %s.\n", ev.Skill, strings.Join(ev.Unexpected, ", "))
					}
				}
			}

			result, err := engine.UpdateRemoteSkills(cfg, targets, flagForce, flagDryRun, skillsDir, cacheDir, onProgress)
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

			totalUpdated := len(result.UpdatedSkills)
			totalSkipped := len(result.SkippedRepos)

			skipMsg := ""
			if totalSkipped > 0 {
				skipMsg = fmt.Sprintf(" (%d repository/repositories were already up to date)", totalSkipped)
			}

			if totalUpdated > 0 {
				if flagDryRun {
					fmt.Fprintf(cmd.OutOrStdout(), "\n%s%sDry run complete: %d skill(s) would be updated.%s%s\n\n", colorBold, colorGreen, totalUpdated, colorReset, skipMsg)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "\n%s%sUpdated %d skill(s).%s%s\n\n", colorBold, colorGreen, totalUpdated, colorReset, skipMsg)
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
