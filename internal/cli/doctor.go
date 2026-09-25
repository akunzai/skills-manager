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

var doctorIsTerminal = tui.IsTerminal
var doctorConfirm = tui.PromptConfirm

func promptReplaceForeignAvailability(out io.Writer, paths []engine.ForeignAvailabilityPath) (bool, error) {
	fmt.Fprintf(out, "\n%sWarning: Doctor found %d unmanaged Agent path(s) that must be removed:%s\n", colorYellow, len(paths), colorReset)
	for _, path := range paths {
		fmt.Fprintf(out, "  %s (%s)\n", models.ToTildePath(path.Path), foreignAvailabilityDetail(path))
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

			var region *presentation.Region
			var approve engine.DoctorReplaceForeign
			if flagFix && doctorIsTerminal() {
				approve = func(paths []engine.ForeignAvailabilityPath) (bool, error) {
					return promptReplaceForeignAvailability(out, paths)
				}
			}
			outcome, runErr := engine.NewDoctorWithCache(cfg, skillsDir, scope.CacheDir).Run(flagFix, func(event engine.DoctorEvent) {
				if region == nil {
					region = presentation.StartRegion(cmd.ErrOrStderr(), "Rebuilding "+countOf(event.Total, "Cache"), event.Total)
				}
				switch {
				case !event.Finished:
					region.Start(presentation.Job{Name: event.Source, Phase: "rebuilding"})
				case event.Failed:
					// The health report below says why.
					region.Fail(event.Source)
				default:
					region.Done(event.Source)
				}
			}, approve)
			region.Stop()
			printHealthReport(out, doctorFindings(outcome.Report))
			if runErr != nil {
				return runErr
			}

			fmt.Fprintln(out, strings.Repeat(tableRule, 60))
			if outcome.Remaining == 0 {
				// Untracked occupancy, unmanaged Agent directories and reserved
				// names are not issues (ADR-0002: 1 means the Scope does not
				// match its Config), so the exit code stays 0 — but saying "top
				// condition" above a standing warning is what made --fix read
				// as broken.
				var notes []string
				for _, warning := range outcome.Warnings {
					notes = append(notes, warningNote(warning))
				}
				if len(notes) > 0 {
					fmt.Fprintf(out, "%s%sNo issues detected. %s.%s\n", colorBold, colorYellow, strings.Join(notes, "; "), colorReset)
				} else {
					fmt.Fprintf(out, "%s%sEverything is in top condition. No issues detected.%s\n", colorBold, colorGreen, colorReset)
				}
				return nil
			}

			if outcome.RecoveryNeeded {
				fmt.Fprintf(out, "%s%sFound %d issue(s). Resolve the reported Cache recovery artifacts, then run 'skills doctor' again.%s\n", colorBold, colorYellow, outcome.Remaining, colorReset)
			} else {
				// Not every issue is repairable by --fix or Sync — an invalid
				// folder and an untracked Skill are not — so this line points
				// at the per-finding next actions instead of promising a
				// blanket repair it cannot deliver.
				fmt.Fprintf(out, "%s%sFound %d issue(s). See the next action for each, or run with --fix.%s\n", colorBold, colorYellow, outcome.Remaining, colorReset)
			}
			// doctor is ADR-0002's third adopter: findings are a state to act
			// on, not a command failure, so they exit 1 without the Error:
			// prefix. 2 stays reserved for work that genuinely broke, and wins
			// when both are present.
			if outcome.RecoveryNeeded {
				return exitError{message: "Cache recovery could not be completed", code: 2}
			}
			if outcome.Failed > 0 {
				return exitError{message: fmt.Sprintf("doctor could not complete %s", countOf(outcome.Failed, "repair")), code: 2}
			}
			return exitError{message: "Scope does not match its Config", code: 1}
		},
	}

	cmd.Flags().BoolVar(&flagFix, "fix", false, "Automatically repair detected issues")

	return cmd
}

// warningNote words one warning kind for doctor's summary line. Every kind in
// engine.DoctorWarningKinds needs a case; TestEveryDoctorWarningKindIsWorded
// fails otherwise.
func warningNote(warning engine.DoctorWarning) string {
	n := warning.Count
	switch warning.Kind {
	case engine.DoctorFindingUntracked:
		return untrackedOccupancy(n) + " not in Config"
	case engine.DoctorFindingUntrackedLink:
		return leftoverSymlinks(n) + " can be pruned"
	case engine.DoctorFindingUnmanagedDirectory:
		if n == 1 {
			return "1 unmanaged Agent directory left as-is"
		}
		return fmt.Sprintf("%d unmanaged Agent directories left as-is", n)
	case engine.DoctorFindingReservedName:
		return countOf(n, "Skill") + " cannot be available to an Agent"
	default:
		return ""
	}
}

func untrackedOccupancy(n int) string {
	if n == 1 {
		return "1 untracked skill is"
	}
	return fmt.Sprintf("%d untracked skills are", n)
}

func leftoverSymlinks(n int) string {
	if n == 1 {
		return "1 leftover symlink"
	}
	return fmt.Sprintf("%d leftover symlinks", n)
}

func printHealthReport(out io.Writer, findings []Finding) {
	for _, f := range findings {
		if f.Blank {
			fmt.Fprintln(out)
		}
		fmt.Fprintf(out, "%s%s%s\n", severityColor(f.Severity), f.Message, colorReset)
	}
}

func severityColor(s Severity) string {
	switch s {
	case SeverityOK:
		return colorGreen
	case SeverityWarning:
		return colorYellow
	case SeverityError:
		return colorRed
	default:
		return colorBold
	}
}
