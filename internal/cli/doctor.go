package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/spf13/cobra"
)

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
			plan := engine.BuildHealthPlan(cfg, skillsDir)
			var result *engine.HealthFixResult
			issuesFound := plan.IssueCount()
			if flagFix {
				applied := engine.ApplyHealthPlan(plan, cfg, skillsDir)
				result = &applied
				issuesFound = engine.RemainingIssues(plan, applied)
			}
			printHealthReport(out, plan, result)

			fmt.Fprintln(out, "\n"+strings.Repeat(tableRule, 60))
			if issuesFound == 0 {
				fmt.Fprintf(out, "%s%sEverything is in top condition. No issues detected.%s\n\n", colorBold, colorGreen, colorReset)
				return nil
			}

			fmt.Fprintf(out, "%s%sFound %d issue(s). Run with --fix or 'skills sync' to repair.%s\n\n", colorBold, colorYellow, issuesFound, colorReset)
			return fmt.Errorf("doctor detected %d issue(s)", issuesFound)
		},
	}

	cmd.Flags().BoolVar(&flagFix, "fix", false, "Automatically repair detected issues")

	return cmd
}

// printHealthReport renders plan.Findings(result): doctor's only job here is
// to pick a color per Severity and print the line.
func printHealthReport(out io.Writer, plan engine.HealthPlan, result *engine.HealthFixResult) {
	for _, f := range plan.Findings(result) {
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
