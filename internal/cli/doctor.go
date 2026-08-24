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
			outcome, runErr := engine.NewDoctorWithCache(cfg, skillsDir, scope.CacheDir).Run(flagFix)
			printHealthReport(out, outcome.Findings)
			if runErr != nil {
				return runErr
			}

			fmt.Fprintln(out, "\n"+strings.Repeat(tableRule, 60))
			if outcome.Remaining == 0 {
				fmt.Fprintf(out, "%s%sEverything is in top condition. No issues detected.%s\n\n", colorBold, colorGreen, colorReset)
				return nil
			}

			fmt.Fprintf(out, "%s%sFound %d issue(s). Run with --fix or 'skills sync' to repair.%s\n\n", colorBold, colorYellow, outcome.Remaining, colorReset)
			return fmt.Errorf("doctor detected %d issue(s)", outcome.Remaining)
		},
	}

	cmd.Flags().BoolVar(&flagFix, "fix", false, "Automatically repair detected issues")

	return cmd
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
