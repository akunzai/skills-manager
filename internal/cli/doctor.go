package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
			issuesFound := 0

			// 1. Check master skills dir
			if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
				fmt.Fprintf(out, "%sMissing master skills directory: %s%s\n", colorRed, models.ToTildePath(skillsDir), colorReset)
				issuesFound++
			} else {
				fmt.Fprintf(out, "%sMaster skills directory: %s%s\n", colorGreen, models.ToTildePath(skillsDir), colorReset)
			}

			// 2. Check broken symlinks in configured agent directories
			fmt.Fprintf(out, "\n%sChecking Agent Directories & Symlinks:%s\n", colorBold, colorReset)
			knownAgents := models.GetAgentsForSkillsDir(skillsDir)
			universalDirs := models.GetUniversalAgentSkillDirs(skillsDir)
			configuredAgents := engine.ConfiguredKnownAgents(cfg, skillsDir)

			configuredNames := make([]string, 0, len(configuredAgents))
			for agentName := range configuredAgents {
				configuredNames = append(configuredNames, agentName)
			}
			sort.Strings(configuredNames)

			for _, agentName := range configuredNames {
				agentDir := configuredAgents[agentName]
				if _, err := os.Stat(agentDir); os.IsNotExist(err) {
					continue
				}

				brokenInAgent, unmanagedBrokenInAgent, physicalInAgent := engine.DiagnoseAgentDirHealth(agentDir, skillsDir)

				if len(brokenInAgent) > 0 {
					fmt.Fprintf(out, "  %s[%s] Broken symlinks:%s %s\n", colorRed, agentName, colorReset, strings.Join(brokenInAgent, ", "))
					if flagFix {
						// Only what --fix could not repair still counts as an issue.
						for _, b := range brokenInAgent {
							if err := os.Remove(filepath.Join(agentDir, b)); err != nil {
								issuesFound++
								fmt.Fprintf(out, "    %sFailed to remove broken symlink %s: %s%s\n", colorRed, b, err, colorReset)
								continue
							}
							fmt.Fprintf(out, "    %sFixed: Removed broken symlink %s.%s\n", colorGreen, b, colorReset)
						}
					} else {
						issuesFound += len(brokenInAgent)
					}
				} else {
					fmt.Fprintf(out, "  %s[%s] Symlinks healthy (%s).%s\n", colorGreen, agentName, models.ToTildePath(agentDir), colorReset)
				}
				if len(unmanagedBrokenInAgent) > 0 {
					issuesFound += len(unmanagedBrokenInAgent)
					fmt.Fprintf(out, "  %sWarning: [%s] Unmanaged broken symlinks were left unchanged:%s %s\n", colorYellow, agentName, colorReset, strings.Join(unmanagedBrokenInAgent, ", "))
				}

				if len(physicalInAgent) > 0 {
					fmt.Fprintf(out, "  %sWarning: [%s] Physical directories found instead of symlinks:%s %s\n", colorYellow, agentName, colorReset, strings.Join(physicalInAgent, ", "))
					if flagFix {
						for _, pName := range physicalInAgent {
							issuesFound++
							fmt.Fprintf(out, "    %sCannot replace unmanaged directory %s in %s.%s\n", colorRed, pName, agentName, colorReset)
						}
					} else {
						issuesFound += len(physicalInAgent)
					}
				}
			}

			// Parent pruning must never escape the scope root: in project scope
			// that is the project directory, in global scope the home directory.
			pruneBoundary := models.GetProjectRootFromSkillsDir(skillsDir)

			// Universal agents read the skills directory directly, so we never link
			// into their directories — but earlier versions and setup scripts did,
			// and those links dangle once a skill is removed. Nothing else in
			// doctor looks at these paths, so the stale links would be invisible.
			universalNames := make([]string, 0, len(universalDirs))
			for agentName := range universalDirs {
				universalNames = append(universalNames, agentName)
			}
			sort.Strings(universalNames)

			for _, agentName := range universalNames {
				agentDir := universalDirs[agentName]
				if _, ok := configuredAgents[agentName]; ok {
					continue
				}
				stale := engine.FindStaleManagedLinks(agentDir, skillsDir)
				if len(stale) == 0 {
					continue
				}
				fmt.Fprintf(out, "  %s[%s] Stale links to removed skills:%s %s\n", colorRed, agentName, colorReset, strings.Join(stale, ", "))
				if flagFix {
					for _, name := range stale {
						if !engine.RemoveManagedSkillLink(filepath.Join(agentDir, name), name, skillsDir) {
							issuesFound++
							fmt.Fprintf(out, "    %sFailed to remove stale link %s%s\n", colorRed, name, colorReset)
							continue
						}
						fmt.Fprintf(out, "    %sFixed: Removed stale link %s.%s\n", colorGreen, name, colorReset)
					}
				} else {
					issuesFound += len(stale)
				}
			}

			leftoverEmpty := engine.LeftoverEmptyAgentDirs(knownAgents, configuredAgents)
			if len(leftoverEmpty) > 0 {
				names := make([]string, 0, len(leftoverEmpty))
				for _, leftover := range leftoverEmpty {
					names = append(names, leftover.Name)
				}
				if flagFix {
					removed := 0
					for _, leftover := range leftoverEmpty {
						if err := engine.RemoveEmptyAgentDir(leftover.Dir, pruneBoundary); err != nil {
							issuesFound++
							fmt.Fprintf(out, "  %sFailed to remove leftover %s dir %s: %s%s\n", colorRed, leftover.Name, models.ToTildePath(leftover.Dir), err, colorReset)
							continue
						}
						removed++
					}
					if removed > 0 {
						fmt.Fprintf(out, "  %sRemoved %d leftover empty agent directories: %s.%s\n", colorGreen, removed, strings.Join(names, ", "), colorReset)
					}
				} else {
					issuesFound += len(leftoverEmpty)
					fmt.Fprintf(out, "  %sWarning: %d leftover empty agent directories (not in defaultAgents):%s %s\n", colorYellow, len(leftoverEmpty), colorReset, strings.Join(names, ", "))
				}
			}

			// 3. Check configured vs installed
			skills := engine.ScanAllSkills(cfg, skillsDir)
			var missing []string
			var untracked []string
			var invalid []string

			for _, s := range skills {
				if !s.IsInstalled {
					missing = append(missing, s.Name)
				} else if s.SourceType == "untracked" {
					untracked = append(untracked, s.Name)
				} else if !s.IsValidSkill {
					invalid = append(invalid, s.Name)
				}
				if !s.IsInstalled || s.SourceType == "untracked" {
					continue
				}
				source := s.Source
				if strings.HasPrefix(s.SourceType, "local_") {
					source = "local"
				}
				missingLinks, unexpectedLinks := engine.AgentLinkDrift(s.Name, source, cfg, skillsDir)
				if len(missingLinks) == 0 && len(unexpectedLinks) == 0 {
					continue
				}
				if flagFix {
					if err := engine.ReconcileAgentSymlinks(s.Name, source, cfg, skillsDir); err != nil {
						issuesFound++
						fmt.Fprintf(out, "\n%sFailed to reconcile availability for %s: %s%s\n", colorRed, s.Name, err, colorReset)
					} else {
						fmt.Fprintf(out, "\n%sFixed availability drift for %s.%s\n", colorGreen, s.Name, colorReset)
					}
					continue
				}
				issuesFound += len(missingLinks) + len(unexpectedLinks)
				if len(missingLinks) > 0 {
					fmt.Fprintf(out, "\n%sAvailability drift for %s; missing links:%s %s\n", colorYellow, s.Name, colorReset, strings.Join(missingLinks, ", "))
				}
				if len(unexpectedLinks) > 0 {
					fmt.Fprintf(out, "\n%sAvailability drift for %s; unexpected links:%s %s\n", colorYellow, s.Name, colorReset, strings.Join(unexpectedLinks, ", "))
				}
			}

			if len(missing) > 0 {
				fmt.Fprintf(out, "\n%sWarning: Configured but missing skills:%s %s\n", colorYellow, colorReset, strings.Join(missing, ", "))
				issuesFound += len(missing)
			}
			if len(untracked) > 0 {
				fmt.Fprintf(out, "\n%sWarning: Untracked skills in %s:%s %s\n", colorYellow, models.ToTildePath(skillsDir), colorReset, strings.Join(untracked, ", "))
			}
			if len(invalid) > 0 {
				fmt.Fprintf(out, "\n%sInstalled folders missing SKILL.md:%s %s\n", colorRed, colorReset, strings.Join(invalid, ", "))
				issuesFound += len(invalid)
			}

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
