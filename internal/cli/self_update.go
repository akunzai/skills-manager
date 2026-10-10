package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"

	"github.com/akunzai/skills-manager/internal/models"
	"github.com/akunzai/skills-manager/internal/updater"
	"github.com/spf13/cobra"
)

var (
	// selfUpdateExecutablePath and selfUpdateGOOS are seams over
	// updater.GetCurrentExecutablePath and runtime.GOOS so tests can
	// simulate a Homebrew, Scoop, or mise install without a real one.
	selfUpdateExecutablePath = updater.GetCurrentExecutablePath
	selfUpdateGOOS           = runtime.GOOS
	// selfUpdateCheck is a seam over updater.CheckSelfUpdate so a test can
	// assert it is never called when a package manager owns this install.
	selfUpdateCheck = updater.CheckSelfUpdate
	// selfUpdateInstall is a seam over updater.DownloadAndInstallBinary so a
	// test can run the install path without replacing a binary.
	selfUpdateInstall = updater.DownloadAndInstallBinary
	// selfUpdateRunPackageManager runs a package manager's upgrade command on
	// the user's terminal. It is a seam so a test never runs brew or mise.
	selfUpdateRunPackageManager = func(argv []string, stdin io.Reader, stdout, stderr io.Writer) error {
		run := exec.Command(argv[0], argv[1:]...)
		run.Stdin, run.Stdout, run.Stderr = stdin, stdout, stderr
		return run.Run()
	}
)

func newSelfUpdateCmd() *cobra.Command {
	var (
		flagCheck   bool
		flagVersion string
		flagForce   bool
		flagDryRun  bool
		flagJSON    bool
		flagYes     bool
	)

	cmd := &cobra.Command{
		Use:     "self-update",
		Aliases: []string{"self-upgrade"},
		Short:   "Replace this CLI with a newer release",
		Long: `Replace this CLI's own binary with a newer GitHub release.

On an interactive terminal, other commands mention a newer release at most once a day.
Set SKILLS_SKIP_SELF_UPDATE_CHECK=1 to skip that check. This command always talks to GitHub.

A Homebrew or mise install is upgraded by running that package manager's
upgrade command, once you confirm or pass --yes. A Scoop install names its
command instead, since Scoop does not update an app while it runs. --check
still reaches GitHub and reports the same command.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Past flag parsing, every failure below is a runtime problem rather
			// than misuse, so reporting it with a usage dump would mislead.
			cmd.SilenceUsage = true
			out := cmd.OutOrStdout()

			pkgMgr := updater.ClassifyExecutablePath(selfUpdateExecutablePath(), selfUpdateGOOS)
			if pkgMgr != nil && !flagCheck {
				// Homebrew's acceptance policy forbids a formula updating
				// itself, a Scoop manifest owns the binary the same way, and
				// mise tracks its own installs, so never download: hand the
				// upgrade to the package manager, or name its command
				// (ADR-0011). The package manager decides whether there is
				// anything to upgrade, so GitHub is not asked first.
				return upgradeThroughPackageManager(cmd, pkgMgr, flagYes, flagDryRun, flagJSON, flagVersion != "")
			}

			if !flagJSON {
				fmt.Fprintf(out, "\n%s%sChecking for skills CLI updates from GitHub Releases...%s\n\n", colorBold, colorCyan, colorReset)
			}

			info, err := selfUpdateCheck(flagVersion)
			if err != nil {
				// This branch already reports the failure itself (as JSON or as a
				// colored message), so silence cobra's own error line to avoid
				// printing the same failure twice.
				cmd.SilenceErrors = true
				if flagJSON {
					printSelfUpdateJSONError(out, err)
				} else {
					fmt.Fprintf(cmd.ErrOrStderr(), "%sFailed to check for updates: %s%s\n\n", errStyle.Red, err, errStyle.Reset)
				}
				return err
			}

			// JSON keeps stdout for one document: a check prints the release
			// it found, and anything past it prints what became of the
			// install once it is over, with text going nowhere.
			text := out
			if flagJSON {
				if flagCheck || (!info.UpdateAvailable && !flagForce) {
					data, _ := json.MarshalIndent(info, "", "  ")
					fmt.Fprintln(out, string(data))
					return nil
				}
				text = io.Discard
			}
			fail := func(err error) error {
				if flagJSON {
					cmd.SilenceErrors = true
					printSelfUpdateJSONError(out, err)
				}
				return err
			}

			cmp := updater.CompareSemver(info.CurrentVersion, info.LatestVersion)
			releaseLabel := "Latest release: "
			if flagVersion != "" {
				releaseLabel = "Target release: "
			}

			if flagCheck {
				fmt.Fprintf(text, "Current version: %s%s%s\n", colorBold, info.CurrentVersion, colorReset)
				fmt.Fprintf(text, "%s %s%s%s\n", releaseLabel, colorBold, info.LatestTag, colorReset)
				if info.UpdateAvailable {
					fmt.Fprintf(text, "\n%s%sUpdate available: %s -> %s%s\n", colorYellow, colorBold, info.CurrentVersion, info.LatestTag, colorReset)
					if pkgMgr != nil {
						fmt.Fprintf(text, "Run '%s%s%s' to upgrade.\n\n", colorBold, pkgMgr.Command, colorReset)
					} else {
						fmt.Fprintf(text, "Run '%sskills self-update%s' to upgrade.\n\n", colorBold, colorReset)
					}
				} else {
					printSelfUpdateCurrent(text, info, cmp, flagVersion != "")
				}
				return nil
			}

			if !info.UpdateAvailable && !flagForce {
				fmt.Fprintf(text, "Current version: %s%s%s\n", colorBold, info.CurrentVersion, colorReset)
				fmt.Fprintf(text, "%s %s%s%s\n", releaseLabel, colorBold, info.LatestTag, colorReset)
				printSelfUpdateCurrent(text, info, cmp, flagVersion != "")
				return nil
			}

			if info.AssetURL == "" {
				return fail(fmt.Errorf("no compatible binary asset found in release %s", info.LatestTag))
			}

			targetPath := selfUpdateExecutablePath()
			fmt.Fprintf(text, "Upgrading skills CLI:\n")
			fmt.Fprintf(text, "  Version:   %s%s%s -> %s%s%s\n", colorYellow, info.CurrentVersion, colorReset, colorGreen, info.LatestTag, colorReset)
			fmt.Fprintf(text, "  Target:    %s\n", models.ToTildePath(targetPath))
			fmt.Fprintf(text, "  Download:  %s\n", info.AssetURL)

			if flagDryRun {
				fmt.Fprintf(text, "\n%s[Dry-run]%s Would download and replace %s with %s\n\n", colorCyan, colorReset, models.ToTildePath(targetPath), info.LatestTag)
				if flagJSON {
					printSelfUpdateJSON(out, selfUpdateJSON{SelfUpdateInfo: info, Status: "dry_run"})
				}
				return nil
			}

			fmt.Fprintf(text, "\nDownloading and installing %s...\n", info.LatestTag)
			installedDest, err := selfUpdateInstall(info.AssetURL, info.ChecksumsURL, targetPath, 30)
			if err != nil {
				return fail(fmt.Errorf("update failed: %w", err))
			}
			if flagJSON {
				printSelfUpdateJSON(out, selfUpdateJSON{SelfUpdateInfo: info, Status: "updated", InstalledPath: installedDest})
			}

			fmt.Fprintf(text, "%sUpdated skills to %s%s%s. (%s)%s\n\n", colorGreen, colorBold, info.LatestTag, colorReset, models.ToTildePath(installedDest), colorReset)
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagCheck, "check", false, "Only check for updates without installing")
	cmd.Flags().StringVar(&flagVersion, "version", "", "Specify target version/tag to install (e.g. v0.2.0)")
	cmd.Flags().BoolVar(&flagForce, "force", false, "Force re-download even if already up to date")
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Preview update without downloading")
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output machine-readable JSON")
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Run the package manager's upgrade command without asking")

	return cmd
}

// selfUpdateJSON is what self-update --json prints once an install it went
// on to was previewed or done: the release it checked, and what became of it.
type selfUpdateJSON struct {
	*updater.SelfUpdateInfo
	Status        string `json:"status"`
	InstalledPath string `json:"installed_path,omitempty"`
}

func printSelfUpdateJSON(out io.Writer, doc selfUpdateJSON) {
	data, _ := json.MarshalIndent(doc, "", "  ")
	fmt.Fprintln(out, string(data))
}

func printSelfUpdateJSONError(out io.Writer, err error) {
	data, _ := json.MarshalIndent(map[string]string{"status": "error", "error": err.Error()}, "", "  ")
	fmt.Fprintln(out, string(data))
}

// upgradeThroughPackageManager runs pkgMgr's upgrade command on the user's
// terminal once they agree. It only names the command when it cannot run it:
// Scoop skips a running app, the command cannot install a pinned version,
// and --json keeps stdout for its one document.
func upgradeThroughPackageManager(cmd *cobra.Command, pkgMgr *updater.PackageManagerInstall, yes, dryRun, jsonOutput, pinned bool) error {
	out := cmd.OutOrStdout()
	if jsonOutput || pinned || !pkgMgr.UpgradesWhileRunning {
		return reportPackageManagerManagedInstall(out, pkgMgr, jsonOutput)
	}
	if dryRun {
		fmt.Fprintf(out, "%s[Dry-run]%s Would run '%s'.\n", colorCyan, colorReset, pkgMgr.Command)
		return nil
	}
	if !yes {
		p := newPrompter(cmd)
		if !p.Interactive() {
			return reportPackageManagerManagedInstall(out, pkgMgr, false)
		}
		confirmed, err := p.Confirm(fmt.Sprintf("%s manages this copy of skills-manager. Run '%s'?", pkgMgr.Name, pkgMgr.Command), false)
		if err != nil {
			return err
		}
		if !confirmed {
			// Declining before anything ran is backing out (ADR-0002).
			fmt.Fprintf(out, "%sOperation cancelled.%s\n", colorYellow, colorReset)
			return nil
		}
	}
	fmt.Fprintf(out, "Running '%s'...\n", pkgMgr.Command)
	if err := selfUpdateRunPackageManager(strings.Fields(pkgMgr.Command), cmd.InOrStdin(), out, cmd.ErrOrStderr()); err != nil {
		return exitError{message: fmt.Sprintf("'%s' failed: %v", pkgMgr.Command, err), code: 2}
	}
	return nil
}

// reportPackageManagerManagedInstall states that pkgMgr already owns this
// install, so self-update refuses to replace it, and names the command to
// run instead. This is a state with a next action, not a failure (ADR-0002):
// exit 1, and main.go's "Error:" prefix stays off for that code.
func reportPackageManagerManagedInstall(out io.Writer, pkgMgr *updater.PackageManagerInstall, jsonOutput bool) error {
	if jsonOutput {
		data, _ := json.MarshalIndent(map[string]string{
			"status":          "package-manager-managed",
			"package_manager": pkgMgr.Name,
			"command":         pkgMgr.Command,
		}, "", "  ")
		fmt.Fprintln(out, string(data))
	} else {
		fmt.Fprintf(out, "\n%s%s%s manages this copy of skills-manager; self-update won't replace it.%s\n", colorBold, colorYellow, pkgMgr.Name, colorReset)
		if !pkgMgr.UpgradesWhileRunning {
			fmt.Fprintf(out, "%s does not update an app while it runs, so run it once skills has exited.\n", pkgMgr.Name)
		}
		fmt.Fprintf(out, "Next: run '%s'.\n\n", pkgMgr.Command)
	}
	return exitError{message: fmt.Sprintf("%s manages this install; run '%s' instead of self-update", pkgMgr.Name, pkgMgr.Command), code: 1}
}

// printSelfUpdateCurrent says that no update is needed. A pinned --version is
// the release asked for, not the latest one, so the running build is only
// ever on it, never ahead of the latest.
func printSelfUpdateCurrent(out io.Writer, info *updater.SelfUpdateInfo, cmp int, pinned bool) {
	switch {
	case pinned:
		fmt.Fprintf(out, "\n%sskills is already on %s.%s\n\n", colorGreen, info.LatestTag, colorReset)
	case cmp > 0:
		fmt.Fprintf(out, "\n%sskills is running a development/pre-release version (%s) ahead of latest release (%s).%s\n\n", colorGreen, info.CurrentVersion, info.LatestTag, colorReset)
	default:
		fmt.Fprintf(out, "\n%sskills is already on the latest version (%s).%s\n\n", colorGreen, info.LatestTag, colorReset)
	}
}
