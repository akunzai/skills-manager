package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/akunzai/skills-manager/internal/presentation"
	"github.com/akunzai/skills-manager/internal/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func stringRuneLen(s string) int {
	return len([]rune(s))
}

func truncateWithEllipsis(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

func padRight(s string, width int) string {
	rLen := stringRuneLen(s)
	if rLen >= width {
		return s
	}
	return s + strings.Repeat(" ", width-rLen)
}

// agentDisplayLabels collapses a Skill's Agents into ls's display labels:
// claude-code and claude fold into one "claude" entry, other Agents drop
// their "-code" suffix, and the internal "agents" marker is excluded.
// Order of first appearance is preserved; duplicates are dropped.
func agentDisplayLabels(agents []string) []string {
	var labels []string
	for _, a := range agents {
		if a == "claude-code" || a == "claude" {
			labels = append(labels, "claude")
			break
		}
	}
	for _, a := range agents {
		if a == "claude-code" || a == "claude" || a == "agents" {
			continue
		}
		cleanA := strings.TrimSuffix(a, "-code")
		found := false
		for _, l := range labels {
			if l == cleanA {
				found = true
				break
			}
		}
		if !found {
			labels = append(labels, cleanA)
		}
	}
	return labels
}

func newLsCmd() *cobra.Command {
	var (
		flagJSON   bool
		flagAgent  string
		flagSource string
	)

	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List installed and configured skills",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Past flag parsing, every failure below is a runtime problem rather
			// than misuse, so reporting it with a usage dump would mislead.
			cmd.SilenceUsage = true
			resolvedScope := ResolveScope()
			configPath, skillsDir := resolvedScope.ConfigPath, resolvedScope.SkillsDir
			out := cmd.OutOrStdout()
			style := presentation.For(out)

			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}

			skills, err := engine.Inventory(cfg, skillsDir)
			if err != nil {
				return err
			}

			if flagAgent != "" {
				filterAgent := models.NormalizeAgentName(flagAgent)
				filtered := make([]models.SkillItem, 0)
				for _, s := range skills {
					if models.IsUniversalAgent(filterAgent, skillsDir) || filterAgent == "agents" || filterAgent == "all" || filterAgent == "universal" {
						if s.IsInstalled {
							filtered = append(filtered, s)
						}
					} else {
						for _, a := range s.Agents {
							if models.NormalizeAgentName(a) == filterAgent {
								filtered = append(filtered, s)
							}
						}
					}
				}
				skills = filtered
			}

			if flagSource != "" {
				pat := strings.ToLower(strings.TrimSpace(flagSource))
				filtered := make([]models.SkillItem, 0)
				for _, s := range skills {
					if strings.Contains(strings.ToLower(s.Source), pat) || strings.Contains(strings.ToLower(s.SourceType), pat) {
						filtered = append(filtered, s)
					}
				}
				skills = filtered
			}

			if flagJSON {
				outList := make([]map[string]interface{}, 0, len(skills))
				for _, s := range skills {
					installedP := s.InstalledPath
					if installedP == "" {
						installedP = filepath.Join(skillsDir, s.Name)
					}
					// Intentionally IsProject alone: --skills-dir pointed
					// somewhere nonstandard, with neither --project nor
					// --global given, does not by itself mean Project Scope.
					scopeLabel := "global"
					if resolvedScope.IsProject {
						scopeLabel = "project"
					}
					outList = append(outList, map[string]interface{}{
						"name":       s.Name,
						"path":       models.ToTildePath(installedP),
						"scope":      scopeLabel,
						"agents":     s.Agents,
						"source":     models.ToTildePath(s.Source),
						"sourceType": s.SourceType,
						"subpath":    s.Subpath,
						"installed":  s.IsInstalled,
						"valid":      s.IsValidSkill,
					})
				}
				data, _ := json.MarshalIndent(outList, "", "  ")
				fmt.Fprintln(out, string(data))
				return nil
			}

			if len(skills) == 0 {
				if flagSource != "" || flagAgent != "" {
					fmt.Fprintf(out, "%sNo skills found matching the specified filters.%s\n", style.Yellow, style.Reset)
				} else {
					fmt.Fprintf(out, "%sNo skills installed or configured.%s\n", style.Yellow, style.Reset)
				}
				return nil
			}

			// Terminal width detection
			termWidth := 94
			if tui.IsTerminal() {
				if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
					termWidth = w
				}
			}
			if termWidth < 80 {
				termWidth = 80
			}

			// Calculate dynamic column widths
			nameWidth := 20
			for _, s := range skills {
				if len(s.Name)+2 > nameWidth {
					nameWidth = len(s.Name) + 2
				}
			}
			if nameWidth > 34 {
				nameWidth = 34
			}

			statusWidth := 24

			agentsWidth := 12
			if termWidth >= 120 {
				maxAgentsLen := 12
				for _, s := range skills {
					joined := strings.Join(agentDisplayLabels(s.Agents), ", ")
					if len(joined) > maxAgentsLen {
						maxAgentsLen = len(joined)
					}
				}
				if maxAgentsLen > 28 {
					maxAgentsLen = 28
				}
				agentsWidth = maxAgentsLen
			}

			sourceWidth := termWidth - nameWidth - agentsWidth - statusWidth - 3
			if sourceWidth < 25 {
				sourceWidth = 25
			}
			totalLineWidth := nameWidth + sourceWidth + agentsWidth + statusWidth + 3

			fmt.Fprintf(out, "\n%s%sSkills (%d total):%s\n\n", style.Bold, style.Cyan, len(skills), style.Reset)
			fmt.Fprintf(out, "%s%s %s %s %s%s\n", style.Bold, padRight("NAME", nameWidth), padRight("SOURCE", sourceWidth), padRight("AGENTS", agentsWidth), padRight("STATUS", statusWidth), style.Reset)
			fmt.Fprintln(out, strings.Repeat(style.Rule, totalLineWidth))

			for _, s := range skills {
				var statusDisplay string
				if s.IsInstalled {
					if s.IsValidSkill {
						statusDisplay = fmt.Sprintf("%s%s%s", style.Green, padRight("Installed", statusWidth), style.Reset)
					} else {
						statusDisplay = fmt.Sprintf("%s%s%s", style.Red, padRight("Invalid (No SKILL.md)", statusWidth), style.Reset)
					}
				} else {
					statusDisplay = fmt.Sprintf("%s%s%s", style.Yellow, padRight("Missing", statusWidth), style.Reset)
				}

				icon := style.SourceIcon(s.SourceType)
				var rawSource string
				if s.SourceType == "untracked" {
					rawSource = icon
				} else if strings.HasPrefix(s.SourceType, "local_symlink") || s.SourceType == "symlink" {
					rawSource = fmt.Sprintf("%s %s", icon, models.ToTildePath(s.Source))
				} else if s.SourceType == "local_command" || s.SourceType == "command" {
					rawSource = fmt.Sprintf("%s %s", icon, s.Source)
				} else if strings.HasPrefix(s.SourceType, "local_") {
					rawSource = fmt.Sprintf("%s %s", icon, models.ToTildePath(s.Source))
				} else {
					rawSource = fmt.Sprintf("%s %s", icon, s.Source)
				}

				sourceCol := padRight(truncateWithEllipsis(rawSource, sourceWidth), sourceWidth)

				targetList := agentDisplayLabels(s.Agents)

				rawTargets := "-"
				if len(targetList) > 0 {
					allAgents := strings.Join(targetList, ", ")
					if len(allAgents) <= agentsWidth {
						rawTargets = allAgents
					} else if len(targetList) == 1 {
						rawTargets = truncateWithEllipsis(targetList[0], agentsWidth)
					} else {
						summary := fmt.Sprintf("%s (+%d)", targetList[0], len(targetList)-1)
						if len(summary) <= agentsWidth {
							rawTargets = summary
						} else {
							rawTargets = truncateWithEllipsis(summary, agentsWidth)
						}
					}
				}

				agentsCol := padRight(rawTargets, agentsWidth)
				var agentsDisplay string
				if len(targetList) > 0 {
					agentsDisplay = agentsCol
				} else {
					agentsDisplay = fmt.Sprintf("%s%s%s", style.Dim, agentsCol, style.Reset)
				}

				nameCol := padRight(truncateWithEllipsis(s.Name, nameWidth), nameWidth)
				nameDisplay := fmt.Sprintf("%s%s%s", style.Bold, nameCol, style.Reset)

				fmt.Fprintf(out, "%s %s %s %s\n", nameDisplay, sourceCol, agentsDisplay, statusDisplay)
			}

			fmt.Fprintln(out, strings.Repeat(style.Rule, totalLineWidth)+"\n")
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output machine-readable JSON")
	cmd.Flags().StringVarP(&flagAgent, "agent", "a", "", "Filter by target agent name")
	cmd.Flags().StringVarP(&flagSource, "source", "s", "", "Filter skills by source repository or type")

	return cmd
}
