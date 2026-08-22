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
	"github.com/akunzai/skills-manager/internal/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const (
	colorCyan   = "\033[96m"
	colorGreen  = "\033[92m"
	colorYellow = "\033[93m"
	colorRed    = "\033[91m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	colorReset  = "\033[0m"
)

func getSourceIcon(sourceType string) string {
	if os.Getenv("NO_NERD_FONT") == "1" || os.Getenv("TERM") == "dumb" {
		switch {
		case sourceType == "symlink" || sourceType == "local_symlink":
			return "🔗"
		case sourceType == "local_command" || sourceType == "command":
			return "⚙️"
		case sourceType == "untracked":
			return "📂"
		default:
			return "📦"
		}
	}
	switch {
	case sourceType == "symlink" || sourceType == "local_symlink":
		return "\U000f0337" // 󰌷
	case sourceType == "local_command" || sourceType == "command":
		return "\U000f018d" // 󰆍
	case sourceType == "untracked":
		return "\U000f024b" // 󰉋
	default:
		return "\U000f02a4" // 󰊤
	}
}

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
			configPath, skillsDir, _ := GetEffectivePaths()

			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}

			skills := engine.ScanAllSkills(cfg, skillsDir)

			if flagAgent != "" {
				filterAgent := models.NormalizeAgentName(flagAgent)
				filtered := make([]models.SkillItem, 0)
				for _, s := range skills {
					if models.IsUniversalAgent(filterAgent) || filterAgent == "agents" || filterAgent == "all" || filterAgent == "universal" {
						if s.IsInstalled {
							filtered = append(filtered, s)
						}
					} else {
						for _, a := range s.LinkedAgents {
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
					scope := "global"
					if flagProject || skillsDir != models.DefaultSkillsDir() {
						scope = "project"
					}
					outList = append(outList, map[string]interface{}{
						"name":       s.Name,
						"path":       models.ToTildePath(installedP),
						"scope":      scope,
						"agents":     s.LinkedAgents,
						"source":     models.ToTildePath(s.Source),
						"sourceType": s.SourceType,
						"subpath":    s.Subpath,
						"installed":  s.IsInstalled,
						"valid":      s.IsValidSkill,
					})
				}
				data, _ := json.MarshalIndent(outList, "", "  ")
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}

			if len(skills) == 0 {
				if flagSource != "" || flagAgent != "" {
					fmt.Printf("%sNo skills found matching the specified filters.%s\n", colorYellow, colorReset)
				} else {
					fmt.Printf("%sNo skills installed or configured.%s\n", colorYellow, colorReset)
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
					var targetList []string
					for _, a := range s.LinkedAgents {
						if a == "claude-code" || a == "claude" {
							targetList = append(targetList, "claude")
						}
					}
					for _, a := range s.LinkedAgents {
						cleanA := strings.TrimSuffix(a, "-code")
						if a != "claude-code" && a != "claude" && a != "agents" {
							found := false
							for _, t := range targetList {
								if t == cleanA {
									found = true
									break
								}
							}
							if !found {
								targetList = append(targetList, cleanA)
							}
						}
					}
					joined := strings.Join(targetList, ", ")
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

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "\n%s%sSkills (%d total):%s\n\n", colorBold, colorCyan, len(skills), colorReset)
			fmt.Fprintf(out, "%s%s %s %s %s%s\n", colorBold, padRight("NAME", nameWidth), padRight("SOURCE", sourceWidth), padRight("AGENTS", agentsWidth), padRight("STATUS", statusWidth), colorReset)
			fmt.Fprintln(out, strings.Repeat("─", totalLineWidth))

			for _, s := range skills {
				var statusDisplay string
				if s.IsInstalled {
					if s.IsValidSkill {
						statusDisplay = fmt.Sprintf("%s%s%s", colorGreen, padRight("Installed", statusWidth), colorReset)
					} else {
						statusDisplay = fmt.Sprintf("%s%s%s", colorRed, padRight("Invalid (No SKILL.md)", statusWidth), colorReset)
					}
				} else {
					statusDisplay = fmt.Sprintf("%s%s%s", colorYellow, padRight("Missing", statusWidth), colorReset)
				}

				icon := getSourceIcon(s.SourceType)
				var rawSource string
				if s.SourceType == "untracked" {
					rawSource = fmt.Sprintf("%s [untracked]", icon)
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

				var targetList []string
				for _, a := range s.LinkedAgents {
					if a == "claude-code" || a == "claude" {
						targetList = append(targetList, "claude")
					}
				}
				for _, a := range s.LinkedAgents {
					cleanA := strings.TrimSuffix(a, "-code")
					if a != "claude-code" && a != "claude" && a != "agents" {
						found := false
						for _, t := range targetList {
							if t == cleanA {
								found = true
								break
							}
						}
						if !found {
							targetList = append(targetList, cleanA)
						}
					}
				}

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
					agentsDisplay = fmt.Sprintf("%s%s%s", colorDim, agentsCol, colorReset)
				}

				nameCol := padRight(truncateWithEllipsis(s.Name, nameWidth), nameWidth)
				nameDisplay := fmt.Sprintf("%s%s%s", colorBold, nameCol, colorReset)

				fmt.Fprintf(out, "%s %s %s %s\n", nameDisplay, sourceCol, agentsDisplay, statusDisplay)
			}

			fmt.Fprintln(out, strings.Repeat("─", totalLineWidth)+"\n")
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output machine-readable JSON")
	cmd.Flags().StringVarP(&flagAgent, "agent", "a", "", "Filter by target agent name")
	cmd.Flags().StringVarP(&flagSource, "source", "s", "", "Filter skills by source repository or type")

	return cmd
}
