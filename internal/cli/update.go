package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
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
		Short:   "Update remote skills to latest versions",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Past flag parsing, every failure below is a runtime problem rather
			// than misuse, so reporting it with a usage dump would mislead.
			cmd.SilenceUsage = true
			configPath, skillsDir, cacheDir := GetEffectivePaths()

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

			onProgress := func(event string, data map[string]interface{}) {
				if flagJSON {
					return
				}
				switch event {
				case "check_start":
					fmt.Fprintf(cmd.OutOrStdout(), "  Checking %v remote repositories in parallel...\n", data["total"])
				case "check_done":
					outdated, _ := data["outdated"].(int)
					upToDate, _ := data["up_to_date"].(int)
					if outdated == 0 {
						fmt.Fprintf(cmd.OutOrStdout(), "  %sAll %d repositories are already up to date.%s\n\n", colorGreen, upToDate, colorReset)
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "  %s%d repository update(s) needed, %d already up to date.%s\n\n", colorCyan, outdated, upToDate, colorReset)
					}
				case "update_start":
					idx := data["index"]
					total := data["total"]
					source := data["source"]
					skills := data["skills"]
					skillsList := ""
					if sList, ok := skills.([]string); ok {
						skillsList = strings.Join(sList, ", ")
					}
					if data["dry_run"] == true {
						fmt.Fprintf(cmd.OutOrStdout(), "  [%v/%v] %s[Dry-run]%s Would update %s%s%s (%s)\n", idx, total, colorCyan, colorReset, colorBold, source, colorReset, skillsList)
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "  [%v/%v] Updating %s%s%s (%s)...\n", idx, total, colorBold, source, colorReset, skillsList)
					}
				case "skill_restored":
					fmt.Fprintf(cmd.OutOrStdout(), "      Restored %s%s%s\n", colorBold, data["skill"], colorReset)
				case "repo_done":
					shaStr := ""
					if sha, ok := data["new_sha"].(string); ok && len(sha) >= 7 {
						shaStr = fmt.Sprintf(" (%s)", sha[:7])
					}
					fmt.Fprintf(cmd.OutOrStdout(), "      %sUpdated %s%s%s%s.%s\n", colorGreen, colorBold, data["source"], colorReset, shaStr, colorReset)
				case "repo_error":
					fmt.Fprintf(cmd.OutOrStdout(), "      %sError updating %s: %s%s\n", colorRed, data["source"], data["error"], colorReset)
				case "would_drift":
					skill, _ := data["skill"].(string)
					if missing, ok := data["missing"].([]string); ok && len(missing) > 0 {
						fmt.Fprintf(cmd.OutOrStdout(), "      [Dry-run] Would link %s to %s.\n", skill, strings.Join(missing, ", "))
					}
					if unexpected, ok := data["unexpected"].([]string); ok && len(unexpected) > 0 {
						fmt.Fprintf(cmd.OutOrStdout(), "      [Dry-run] Would unlink %s from %s.\n", skill, strings.Join(unexpected, ", "))
					}
				}
			}

			result, err := engine.UpdateRemoteSkills(cfg, targets, flagForce, flagDryRun, skillsDir, cacheDir, onProgress)
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
