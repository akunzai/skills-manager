package cli

import (
	"encoding/json"
	"fmt"

	"github.com/akunzai/skills-manager/internal/models"
	"github.com/akunzai/skills-manager/internal/updater"
	"github.com/spf13/cobra"
)

func newSelfUpdateCmd() *cobra.Command {
	var (
		flagCheck   bool
		flagVersion string
		flagForce   bool
		flagDryRun  bool
		flagJSON    bool
	)

	cmd := &cobra.Command{
		Use:     "self-update",
		Aliases: []string{"self-upgrade"},
		Short:   "Update skills CLI itself to latest release",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Past flag parsing, every failure below is a runtime problem rather
			// than misuse, so reporting it with a usage dump would mislead.
			cmd.SilenceUsage = true
			out := cmd.OutOrStdout()
			if !flagJSON {
				fmt.Fprintf(out, "\n%s%sChecking for skills CLI updates from GitHub Releases...%s\n\n", colorBold, colorCyan, colorReset)
			}

			info, err := updater.CheckSelfUpdate(flagVersion)
			if err != nil {
				// This branch already reports the failure itself (as JSON or as a
				// colored message), so silence cobra's own error line to avoid
				// printing the same failure twice.
				cmd.SilenceErrors = true
				if flagJSON {
					data, _ := json.MarshalIndent(map[string]string{"status": "error", "error": err.Error()}, "", "  ")
					fmt.Fprintln(out, string(data))
				} else {
					fmt.Fprintf(cmd.ErrOrStderr(), "%sFailed to check for updates: %s%s\n\n", errStyle.Red, err, errStyle.Reset)
				}
				return err
			}

			if flagJSON {
				data, _ := json.MarshalIndent(info, "", "  ")
				fmt.Fprintln(out, string(data))
				if flagCheck || (!info.UpdateAvailable && !flagForce) {
					return nil
				}
			}

			cmp := updater.CompareSemver(info.CurrentVersion, info.LatestVersion)

			if flagCheck {
				fmt.Fprintf(out, "Current version: %s%s%s\n", colorBold, info.CurrentVersion, colorReset)
				fmt.Fprintf(out, "Latest release:  %s%s%s\n", colorBold, info.LatestTag, colorReset)
				if info.UpdateAvailable {
					fmt.Fprintf(out, "\n%s%sUpdate available: %s -> %s%s\n", colorYellow, colorBold, info.CurrentVersion, info.LatestTag, colorReset)
					fmt.Fprintf(out, "Run '%s%sskills self-update%s' to upgrade.\n\n", colorBold, colorReset, colorReset)
				} else if cmp > 0 {
					fmt.Fprintf(out, "\n%sskills is running a development/pre-release version (%s) ahead of latest release (%s).%s\n\n", colorGreen, info.CurrentVersion, info.LatestTag, colorReset)
				} else {
					fmt.Fprintf(out, "\n%sskills is already on the latest version (%s).%s\n\n", colorGreen, info.LatestTag, colorReset)
				}
				return nil
			}

			if !info.UpdateAvailable && !flagForce {
				fmt.Fprintf(out, "Current version: %s%s%s\n", colorBold, info.CurrentVersion, colorReset)
				fmt.Fprintf(out, "Latest release:  %s%s%s\n", colorBold, info.LatestTag, colorReset)
				if cmp > 0 {
					fmt.Fprintf(out, "\n%sskills is running a development/pre-release version (%s) ahead of latest release (%s).%s\n\n", colorGreen, info.CurrentVersion, info.LatestTag, colorReset)
				} else {
					fmt.Fprintf(out, "\n%sskills is already on the latest version (%s).%s\n\n", colorGreen, info.LatestTag, colorReset)
				}
				return nil
			}

			if info.AssetURL == "" {
				return fmt.Errorf("no compatible binary asset found in release %s", info.LatestTag)
			}

			targetPath := updater.GetCurrentExecutablePath()
			fmt.Fprintf(out, "Upgrading skills CLI:\n")
			fmt.Fprintf(out, "  Version:   %s%s%s -> %s%s%s\n", colorYellow, info.CurrentVersion, colorReset, colorGreen, info.LatestTag, colorReset)
			fmt.Fprintf(out, "  Target:    %s\n", models.ToTildePath(targetPath))
			fmt.Fprintf(out, "  Download:  %s\n", info.AssetURL)

			if flagDryRun {
				fmt.Fprintf(out, "\n%s[Dry-run]%s Would download and replace %s with %s\n\n", colorCyan, colorReset, models.ToTildePath(targetPath), info.LatestTag)
				return nil
			}

			fmt.Fprintf(out, "\nDownloading and installing %s...\n", info.LatestTag)
			installedDest, err := updater.DownloadAndInstallBinary(info.AssetURL, targetPath, 30)
			if err != nil {
				return fmt.Errorf("update failed: %w", err)
			}

			fmt.Fprintf(out, "%sUpdated skills to %s%s%s. (%s)%s\n\n", colorGreen, colorBold, info.LatestTag, colorReset, models.ToTildePath(installedDest), colorReset)
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagCheck, "check", false, "Only check for updates without installing")
	cmd.Flags().StringVar(&flagVersion, "version", "", "Specify target version/tag to install (e.g. v0.2.0)")
	cmd.Flags().BoolVar(&flagForce, "force", false, "Force re-download even if already up to date")
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Preview update without downloading")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output machine-readable JSON")

	return cmd
}
