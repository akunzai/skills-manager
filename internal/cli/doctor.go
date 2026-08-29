package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/akunzai/skills-manager/internal/presentation"
	"github.com/akunzai/skills-manager/internal/tui"
	"github.com/spf13/cobra"
)

var startDoctorProgress = presentation.StartProgress
var doctorIsTerminal = tui.IsTerminal
var doctorConfirm = tui.PromptConfirm

func promptReplaceForeignAvailability(out io.Writer, paths []engine.ForeignAvailabilityPath) (bool, error) {
	fmt.Fprintf(out, "\n%sWarning: Doctor found %d unmanaged Agent path(s) that must be removed:%s\n", colorYellow, len(paths), colorReset)
	for _, path := range paths {
		fmt.Fprintf(out, "  %s (%s)\n", models.ToTildePath(path.Path), path.Detail())
	}
	fmt.Fprintln(out)
	return doctorConfirm("Replace these paths with managed Availability?", false)
}

func newDoctorCmd() *cobra.Command {
	var flagFix bool

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose and repair skills health",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Past flag parsing, every failure below is a runtime problem rather
			// than misuse, so reporting it with a usage dump would mislead.
			cmd.SilenceUsage = true
			out := cmd.OutOrStdout()
			scope := ResolveScope()
			configPath, skillsDir := scope.ConfigPath, scope.SkillsDir

			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}

			fmt.Fprintf(out, "\n%s%sDiagnosing skills health...%s\n\n", colorBold, colorCyan, colorReset)
			var progress *presentation.Progress
			var approve engine.DoctorReplaceForeign
			if flagFix && doctorIsTerminal() {
				approve = func(paths []engine.ForeignAvailabilityPath) (bool, error) {
					return promptReplaceForeignAvailability(out, paths)
				}
			}
			outcome, runErr := engine.NewDoctorWithCache(cfg, skillsDir, scope.CacheDir).RunWithRepairApproval(flagFix, func(event engine.DoctorEvent) {
				progress.Stop()
				progress = startDoctorProgress(cmd.ErrOrStderr(), fmt.Sprintf("[%d/%d] Rebuilding %s Cache...", event.Index, event.Total, event.Source))
			}, approve)
			progress.Stop()
			printHealthReport(out, outcome.Findings)
			if runErr != nil {
				return runErr
			}

			fmt.Fprintln(out, "\n"+strings.Repeat(tableRule, 60))
			if outcome.Remaining == 0 {
				// An untracked Skill is not counted as an issue (ADR-0002: 1
				// means the Scope does not match its Config), so the exit code
				// stays 0 — but saying "top condition" above a yellow warning
				// doctor cannot act on is what makes --fix read as broken.
				if outcome.Untracked > 0 {
					fmt.Fprintf(out, "%s%sNo issues detected. %s your decision.%s\n\n", colorBold, colorYellow, untrackedPending(outcome.Untracked), colorReset)
					return nil
				}
				fmt.Fprintf(out, "%s%sEverything is in top condition. No issues detected.%s\n\n", colorBold, colorGreen, colorReset)
				return nil
			}

			if outcome.RecoveryNeeded {
				fmt.Fprintf(out, "%s%sFound %d issue(s). Resolve the reported Cache recovery artifacts, then run 'skills doctor' again.%s\n\n", colorBold, colorYellow, outcome.Remaining, colorReset)
			} else {
				// Not every issue is repairable by --fix or Sync — an invalid
				// folder and an untracked Skill are not — so this line points
				// at the per-finding next actions instead of promising a
				// blanket repair it cannot deliver.
				fmt.Fprintf(out, "%s%sFound %d issue(s). See the next action for each, or run with --fix.%s\n\n", colorBold, colorYellow, outcome.Remaining, colorReset)
			}
			return fmt.Errorf("doctor detected %d issue(s)", outcome.Remaining)
		},
	}

	cmd.Flags().BoolVar(&flagFix, "fix", false, "Automatically repair detected issues")

	return cmd
}

// untrackedPending renders the count of Skills waiting on the user, without
// their names: those are already on the warning line above.
func untrackedPending(n int) string {
	if n == 1 {
		return "1 untracked skill needs"
	}
	return fmt.Sprintf("%d untracked skills need", n)
}

func printHealthReport(out io.Writer, findings []engine.Finding) {
	for _, f := range findings {
		if f.Blank {
			fmt.Fprintln(out)
		}
		fmt.Fprintf(out, "%s%s%s\n", severityColor(f.Severity), f.Message, colorReset)
	}
}

func severityColor(s engine.Severity) string {
	switch s {
	case engine.SeverityOK:
		return colorGreen
	case engine.SeverityWarning:
		return colorYellow
	case engine.SeverityError:
		return colorRed
	default:
		return colorBold
	}
}
