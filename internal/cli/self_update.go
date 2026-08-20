package cli

import (
	"encoding/json"
	"fmt"

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
			if !flagJSON {
				fmt.Printf("\n%s%s🔍 Checking for skills CLI updates from GitHub Releases...%s\n\n", colorBold, colorCyan, colorReset)
			}

			info, err := updater.CheckSelfUpdate(flagVersion)
			if err != nil {
				if flagJSON {
					data, _ := json.MarshalIndent(map[string]string{"status": "error", "error": err.Error()}, "", "  ")
					fmt.Println(string(data))
				} else {
					fmt.Printf("%s✖ Failed to check for updates: %s%s\n\n", colorRed, err, colorReset)
				}
				return err
			}

			if flagJSON {
				data, _ := json.MarshalIndent(info, "", "  ")
				fmt.Println(string(data))
				if flagCheck || (!info.UpdateAvailable && !flagForce) {
					return nil
				}
			}

			cmp := updater.CompareSemver(info.CurrentVersion, info.LatestVersion)

			if flagCheck {
				fmt.Printf("Current version: %s%s%s\n", colorBold, info.CurrentVersion, colorReset)
				fmt.Printf("Latest release:  %s%s%s\n", colorBold, info.LatestTag, colorReset)
				if info.UpdateAvailable {
					fmt.Printf("\n%s%s✨ Update available: %s -> %s%s\n", colorYellow, colorBold, info.CurrentVersion, info.LatestTag, colorReset)
					fmt.Printf("Run '%s%sskills self-update%s' to upgrade.\n\n", colorBold, colorReset, colorReset)
				} else if cmp > 0 {
					fmt.Printf("\n%s✔ skills is running a development/pre-release version (%s) ahead of latest release (%s).%s\n\n", colorGreen, info.CurrentVersion, info.LatestTag, colorReset)
				} else {
					fmt.Printf("\n%s✔ skills is already on the latest version (%s).%s\n\n", colorGreen, info.LatestTag, colorReset)
				}
				return nil
			}

			if !info.UpdateAvailable && !flagForce {
				fmt.Printf("Current version: %s%s%s\n", colorBold, info.CurrentVersion, colorReset)
				fmt.Printf("Latest release:  %s%s%s\n", colorBold, info.LatestTag, colorReset)
				if cmp > 0 {
					fmt.Printf("\n%s✔ skills is running a development/pre-release version (%s) ahead of latest release (%s).%s\n\n", colorGreen, info.CurrentVersion, info.LatestTag, colorReset)
				} else {
					fmt.Printf("\n%s✔ skills is already on the latest version (%s).%s\n\n", colorGreen, info.LatestTag, colorReset)
				}
				return nil
			}

			if info.AssetURL == "" {
				return fmt.Errorf("no compatible binary asset found in release %s", info.LatestTag)
			}

			targetPath := updater.GetCurrentExecutablePath()
			fmt.Printf("Upgrading skills CLI:\n")
			fmt.Printf("  Version:   %s%s%s -> %s%s%s\n", colorYellow, info.CurrentVersion, colorReset, colorGreen, info.LatestTag, colorReset)
			fmt.Printf("  Target:    %s\n", targetPath)
			fmt.Printf("  Download:  %s\n", info.AssetURL)

			if flagDryRun {
				fmt.Printf("\n%sℹ [Dry-run]%s Would download and replace %s with %s\n\n", colorCyan, colorReset, targetPath, info.LatestTag)
				return nil
			}

			fmt.Printf("\n📥 Downloading and installing %s...\n", info.LatestTag)
			installedDest, err := updater.DownloadAndInstallBinary(info.AssetURL, targetPath, 30)
			if err != nil {
				return fmt.Errorf("update failed: %w", err)
			}

			fmt.Printf("%s✔ Successfully updated skills to %s%s%s! (%s)\n\n", colorGreen, colorBold, info.LatestTag, colorReset, installedDest)
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
