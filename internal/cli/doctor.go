package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
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
			configPath, skillsDir, _ := GetEffectivePaths()

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

func printHealthReport(out io.Writer, plan engine.HealthPlan, result *engine.HealthFixResult) {
	if plan.MasterMissing {
		fmt.Fprintf(out, "%sMissing master skills directory: %s%s\n", colorRed, models.ToTildePath(plan.SkillsDir), colorReset)
	} else {
		fmt.Fprintf(out, "%sMaster skills directory: %s%s\n", colorGreen, models.ToTildePath(plan.SkillsDir), colorReset)
	}

	fmt.Fprintf(out, "\n%sChecking Agent Directories & Symlinks:%s\n", colorBold, colorReset)
	for _, agent := range plan.Agents {
		if len(agent.Broken) > 0 {
			fmt.Fprintf(out, "  %s[%s] Broken symlinks:%s %s\n", colorRed, agent.Name, colorReset, strings.Join(agent.Broken, ", "))
			if result != nil {
				printBrokenFixes(out, agent.Name, *result)
			}
		} else {
			fmt.Fprintf(out, "  %s[%s] Symlinks healthy (%s).%s\n", colorGreen, agent.Name, models.ToTildePath(agent.Dir), colorReset)
		}
		if len(agent.UnmanagedBroken) > 0 {
			fmt.Fprintf(out, "  %sWarning: [%s] Unmanaged broken symlinks were left unchanged:%s %s\n", colorYellow, agent.Name, colorReset, strings.Join(agent.UnmanagedBroken, ", "))
		}
		if len(agent.Physical) > 0 {
			fmt.Fprintf(out, "  %sWarning: [%s] Physical directories found instead of symlinks:%s %s\n", colorYellow, agent.Name, colorReset, strings.Join(agent.Physical, ", "))
			if result != nil {
				for _, pName := range agent.Physical {
					fmt.Fprintf(out, "    %sCannot replace unmanaged directory %s in %s.%s\n", colorRed, pName, agent.Name, colorReset)
				}
			}
		}
	}

	for _, stale := range plan.StaleUniversal {
		fmt.Fprintf(out, "  %s[%s] Stale links to removed skills:%s %s\n", colorRed, stale.Agent, colorReset, strings.Join(stale.Names, ", "))
		if result != nil {
			printStaleFixes(out, stale.Agent, *result)
		}
	}

	if result == nil && len(plan.LeftoverEmpty) > 0 {
		names := leftoverNames(plan.LeftoverEmpty)
		fmt.Fprintf(out, "  %sWarning: %d leftover empty agent directories (not in defaultAgents):%s %s\n", colorYellow, len(plan.LeftoverEmpty), colorReset, strings.Join(names, ", "))
	}
	if result != nil {
		for _, fix := range result.FailedLeftover {
			fmt.Fprintf(out, "  %sFailed to remove leftover %s dir %s: %s%s\n", colorRed, fix.Agent, models.ToTildePath(fix.Name), fix.Err, colorReset)
		}
		if len(result.RemovedLeftover) > 0 {
			fmt.Fprintf(out, "  %sRemoved %d leftover empty agent directories: %s.%s\n", colorGreen, len(result.RemovedLeftover), strings.Join(leftoverNames(plan.LeftoverEmpty), ", "), colorReset)
		}
	}

	if result == nil {
		for _, d := range plan.Drift {
			if len(d.Missing) > 0 {
				fmt.Fprintf(out, "\n%sAvailability drift for %s; missing links:%s %s\n", colorYellow, d.Skill, colorReset, strings.Join(d.Missing, ", "))
			}
			if len(d.Unexpected) > 0 {
				fmt.Fprintf(out, "\n%sAvailability drift for %s; unexpected links:%s %s\n", colorYellow, d.Skill, colorReset, strings.Join(d.Unexpected, ", "))
			}
		}
	} else {
		for _, skill := range result.FixedDrift {
			fmt.Fprintf(out, "\n%sFixed availability drift for %s.%s\n", colorGreen, skill, colorReset)
		}
		for _, fix := range result.FailedDrift {
			fmt.Fprintf(out, "\n%sFailed to reconcile availability for %s: %s%s\n", colorRed, fix.Name, fix.Err, colorReset)
		}
	}

	if len(plan.Missing) > 0 {
		fmt.Fprintf(out, "\n%sWarning: Configured but missing skills:%s %s\n", colorYellow, colorReset, strings.Join(plan.Missing, ", "))
	}
	if len(plan.Untracked) > 0 {
		fmt.Fprintf(out, "\n%sWarning: Untracked skills in %s:%s %s\n", colorYellow, models.ToTildePath(plan.SkillsDir), colorReset, strings.Join(plan.Untracked, ", "))
	}
	if len(plan.Invalid) > 0 {
		fmt.Fprintf(out, "\n%sInstalled folders missing SKILL.md:%s %s\n", colorRed, colorReset, strings.Join(plan.Invalid, ", "))
	}
	for _, ref := range plan.UnknownAgents {
		where := "settings." + ref.Field
		if ref.Skill != "" {
			where = fmt.Sprintf("settings.availability.%s.%s", ref.Skill, ref.Field)
		}
		fmt.Fprintf(out, "\n%sWarning: unknown agent %q in %s%s\n", colorYellow, ref.Agent, where, colorReset)
	}
}

func leftoverNames(dirs []engine.AgentDir) []string {
	names := make([]string, 0, len(dirs))
	for _, leftover := range dirs {
		names = append(names, leftover.Name)
	}
	return names
}

func printBrokenFixes(out io.Writer, agent string, result engine.HealthFixResult) {
	for _, fix := range result.RemovedBroken {
		if fix.Agent == agent {
			fmt.Fprintf(out, "    %sFixed: Removed broken symlink %s.%s\n", colorGreen, fix.Name, colorReset)
		}
	}
	for _, fix := range result.FailedBroken {
		if fix.Agent == agent {
			fmt.Fprintf(out, "    %sFailed to remove broken symlink %s: %s%s\n", colorRed, fix.Name, fix.Err, colorReset)
		}
	}
}

func printStaleFixes(out io.Writer, agent string, result engine.HealthFixResult) {
	for _, fix := range result.RemovedStale {
		if fix.Agent == agent {
			fmt.Fprintf(out, "    %sFixed: Removed stale link %s.%s\n", colorGreen, fix.Name, colorReset)
		}
	}
	for _, fix := range result.FailedStale {
		if fix.Agent == agent {
			fmt.Fprintf(out, "    %sFailed to remove stale link %s%s\n", colorRed, fix.Name, colorReset)
		}
	}
}
