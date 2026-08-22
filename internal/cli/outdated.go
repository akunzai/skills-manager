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

func newOutdatedCmd() *cobra.Command {
	var flagJSON bool

	cmd := &cobra.Command{
		Use:     "outdated",
		Aliases: []string{"check", "check-update"},
		Short:   "Check for new versions in remote skill repositories",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Past flag parsing, every failure below is a runtime problem rather
			// than misuse, so reporting it with a usage dump would mislead.
			cmd.SilenceUsage = true
			configPath, _, cacheDir := GetEffectivePaths()

			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}

			if len(cfg.Remote) == 0 {
				if flagJSON {
					fmt.Println("[]")
				} else {
					fmt.Printf("%sNo remote repositories configured in %s.%s\n", colorYellow, filepath.Base(configPath), colorReset)
				}
				return nil
			}

			if !flagJSON {
				fmt.Printf("\n%s%sChecking remote repositories for updates...%s\n\n", colorBold, colorCyan, colorReset)
			}

			results := engine.CheckAllRemoteSkillsOutdated(cfg, cacheDir, 8)

			if flagJSON {
				data, _ := json.MarshalIndent(results, "", "  ")
				fmt.Println(string(data))
				return nil
			}

			fmt.Printf("%s%-40s %-12s %-12s %s%s\n", colorBold, "REPOSITORY / SKILL", "CURRENT", "LATEST", "STATUS", colorReset)
			fmt.Println(strings.Repeat(tableRule, 80))

			outdatedCount := 0
			upToDateCount := 0
			errorCount := 0

			for _, r := range results {
				localRaw := "none"
				if len(r.LocalSHA) >= 7 {
					localRaw = r.LocalSHA[:7]
				}
				remoteRaw := "none"
				if len(r.RemoteSHA) >= 7 {
					remoteRaw = r.RemoteSHA[:7]
				}

				localDisplay := fmt.Sprintf("%-12s", localRaw)
				if r.LocalSHA == "" {
					localDisplay = fmt.Sprintf("%s%-12s%s", colorDim, localRaw, colorReset)
				}
				remoteDisplay := fmt.Sprintf("%-12s", remoteRaw)
				if r.RemoteSHA == "" {
					remoteDisplay = fmt.Sprintf("%s%-12s%s", colorDim, remoteRaw, colorReset)
				}

				var statusDisplay string
				switch r.Status {
				case "update_available":
					outdatedCount++
					statusDisplay = fmt.Sprintf("%s%sUpdate available%s", colorYellow, colorBold, colorReset)
				case "up_to_date":
					upToDateCount++
					statusDisplay = fmt.Sprintf("%sUp to date%s", colorGreen, colorReset)
				case "not_cached", "not_installed":
					outdatedCount++
					statusDisplay = fmt.Sprintf("%sNot cached (Needs sync)%s", colorCyan, colorReset)
				default:
					errorCount++
					statusDisplay = fmt.Sprintf("%sCheck failed%s", colorRed, colorReset)
				}

				fmt.Printf("%s%-40s%s %s %s %s\n", colorBold, r.Source, colorReset, localDisplay, remoteDisplay, statusDisplay)

				for i, sk := range r.Skills {
					prefix := "  " + treeBranch + " "
					if i == len(r.Skills)-1 {
						prefix = "  " + treeLastBranch + " "
					}
					fmt.Printf("%s%s%s%s\n", colorDim, prefix, sk, colorReset)
				}
			}

			fmt.Println(strings.Repeat(tableRule, 80))

			var summaryParts []string
			if outdatedCount > 0 {
				summaryParts = append(summaryParts, fmt.Sprintf("%s%s%d update(s) available%s", colorYellow, colorBold, outdatedCount, colorReset))
			}
			if upToDateCount > 0 {
				summaryParts = append(summaryParts, fmt.Sprintf("%s%d up to date%s", colorGreen, upToDateCount, colorReset))
			}
			if errorCount > 0 {
				summaryParts = append(summaryParts, fmt.Sprintf("%s%d error(s)%s", colorRed, errorCount, colorReset))
			}

			fmt.Printf("Summary: %s\n", strings.Join(summaryParts, ", "))
			if outdatedCount > 0 {
				fmt.Printf("\nRun '%s%sskills update%s' to upgrade outdated skills.\n\n", colorBold, colorReset, colorReset)
			} else {
				fmt.Printf("\n%sAll skills are up to date.%s\n\n", colorGreen, colorReset)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output machine-readable JSON")

	return cmd
}
