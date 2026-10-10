package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/akunzai/skills-manager/internal/presentation"
	"github.com/akunzai/skills-manager/internal/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// lsJSONItem is ls --json's per-Skill row, a typed DTO the CLI owns so
// models.SkillItem can stay an engine row (ADR-0008). Fields are ordered
// alphabetically by JSON key, matching the key order encoding/json emitted
// for the map[string]any this struct replaced, so the JSON contract stays
// byte for byte identical: a struct marshals in field declaration order,
// where a map's string keys were already sorted.
type lsJSONItem struct {
	Agents     []string           `json:"agents"`
	Installed  bool               `json:"installed"`
	Name       string             `json:"name"`
	Path       string             `json:"path"`
	Scope      string             `json:"scope"`
	Signed     bool               `json:"signed"`
	Source     string             `json:"source"`
	SourceType string             `json:"sourceType"`
	Status     models.SkillStatus `json:"status"`
	Subpath    string             `json:"subpath"`
	Unverified bool               `json:"unverified"`
	Valid      bool               `json:"valid"`
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

// agentDisplayLabels collapses a Skill's Agents into ls's display labels:
// claude-code and claude fold into one "claude" entry, other Agents drop
// their "-code" suffix, and the internal "agents" marker is excluded.
// Order of first appearance is preserved; duplicates are dropped.
func agentDisplayLabels(agents []string) []string {
	var labels []string
	isClaude := func(a string) bool { return a == "claude-code" || a == "claude" }
	if slices.ContainsFunc(agents, isClaude) {
		labels = append(labels, "claude")
	}
	for _, a := range agents {
		if isClaude(a) || a == "agents" {
			continue
		}
		cleanA := strings.TrimSuffix(a, "-code")
		if !slices.Contains(labels, cleanA) {
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
			resolvedScope := ResolveScope(cmd)
			configPath, skillsDir := resolvedScope.ConfigPath, resolvedScope.SkillsDir
			out := cmd.OutOrStdout()
			style := presentation.For(out)

			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}

			inv, err := engine.LoadInventory(cfg, skillsDir)
			if err != nil {
				return err
			}
			// Only a remote Skill has a Baseline, and it records whether the
			// applied copy was signed.
			skills := inv.SkillItems(engine.OpenBaselines(skillsDir))

			if flagAgent != "" {
				filterAgent := models.NormalizeAgentName(flagAgent)
				// These three aliases mean "every installed Skill" and are not
				// Agent names, so they never reach AvailableTo; a real
				// Automatically available Agent (e.g. gemini) gets the same
				// answer from there instead.
				if filterAgent == "agents" || filterAgent == "all" || filterAgent == "universal" {
					filtered := make([]models.SkillItem, 0)
					for _, s := range skills {
						if s.IsInstalled {
							filtered = append(filtered, s)
						}
					}
					skills = filtered
				} else {
					visible := make(map[string]bool)
					for _, s := range inv.AvailableTo(filterAgent) {
						visible[s.Name] = true
					}
					filtered := make([]models.SkillItem, 0)
					for _, s := range skills {
						if visible[s.Name] {
							filtered = append(filtered, s)
						}
					}
					skills = filtered
				}
			}

			if flagSource != "" {
				pat := strings.ToLower(strings.TrimSpace(flagSource))
				filtered := make([]models.SkillItem, 0)
				for _, s := range skills {
					if strings.Contains(strings.ToLower(s.Source), pat) || strings.Contains(strings.ToLower(lsSourceType(s)), pat) {
						filtered = append(filtered, s)
					}
				}
				skills = filtered
			}

			if flagJSON {
				// Intentionally IsProject alone: --skills-dir pointed somewhere
				// nonstandard, with neither --project nor --global given, does
				// not by itself mean Project Scope.
				scopeLabel := "global"
				if resolvedScope.IsProject {
					scopeLabel = "project"
				}
				outList := make([]lsJSONItem, 0, len(skills))
				for _, s := range skills {
					outList = append(outList, lsJSONItem{
						Name:       s.Name,
						Path:       models.ToTildePath(s.InstalledPath),
						Scope:      scopeLabel,
						Agents:     s.Agents,
						Source:     models.ToTildePath(s.Source),
						SourceType: lsSourceType(s),
						Subpath:    s.Subpath,
						Installed:  s.IsInstalled,
						Valid:      s.IsValidSkill,
						Status:     s.Status,
						Signed:     s.Signed,
						Unverified: s.Unverified,
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
				if stringRuneLen(s.Name)+2 > nameWidth {
					nameWidth = stringRuneLen(s.Name) + 2
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
					if stringRuneLen(joined) > maxAgentsLen {
						maxAgentsLen = stringRuneLen(joined)
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
				label, color := lsStatus(s)
				statusDisplay := fmt.Sprintf("%s%s%s", color(style), padRight(label, statusWidth), style.Reset)

				icon := style.SourceIcon(s.Kind)
				var rawSource string
				switch s.Kind {
				case models.InventoryUntracked:
					rawSource = icon
				case models.InventorySymlink, models.InventoryUntrackedLink:
					rawSource = fmt.Sprintf("%s %s", icon, models.ToTildePath(s.Source))
				default:
					rawSource = fmt.Sprintf("%s %s", icon, s.Source)
				}
				// Only a signed copy, and one applied from Trusted content, is
				// marked; an unsigned one is the norm. The mark survives
				// truncation, since it is what the row asserts.
				sourceCol := padRight(truncateWithEllipsis(rawSource, sourceWidth), sourceWidth)
				if s.Signed {
					mark := " " + style.SignedMark()
					sourceCol = padRight(truncateWithEllipsis(rawSource, sourceWidth-stringRuneLen(mark))+mark, sourceWidth)
				} else if s.Unverified {
					mark := style.UnverifiedMark()
					sourceCol = padRight(truncateWithEllipsis(rawSource, sourceWidth-stringRuneLen(mark)-1)+" "+style.Yellow+mark+style.Reset, sourceWidth+len(style.Yellow)+len(style.Reset))
				}

				targetList := agentDisplayLabels(s.Agents)

				rawTargets := "-"
				if len(targetList) > 0 {
					allAgents := strings.Join(targetList, ", ")
					if stringRuneLen(allAgents) <= agentsWidth {
						rawTargets = allAgents
					} else if len(targetList) == 1 {
						rawTargets = truncateWithEllipsis(targetList[0], agentsWidth)
					} else {
						summary := fmt.Sprintf("%s (+%d)", targetList[0], len(targetList)-1)
						if stringRuneLen(summary) <= agentsWidth {
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

// lsStatus words the status Inventory classified, so ls and Doctor name one
// entry the same way. An Untracked entry keeps the old wording, since its
// Source column already says it is Untracked.
func lsStatus(s models.SkillItem) (string, func(presentation.Style) string) {
	green := func(st presentation.Style) string { return st.Green }
	yellow := func(st presentation.Style) string { return st.Yellow }
	red := func(st presentation.Style) string { return st.Red }
	switch {
	case s.Status == models.SkillStatusMissing:
		return "Missing", yellow
	case s.Status == models.SkillStatusStub:
		return "Stub (text file)", red
	case s.Status == models.SkillStatusIllegalLocal:
		return "Source inside skills dir", red
	case s.Status == models.SkillStatusUntrackedLink && !s.IsValidSkill:
		return "Broken link", red
	case !s.IsValidSkill:
		return "Invalid (No SKILL.md)", red
	default:
		return "Installed", green
	}
}

// lsSourceType is the sourceType ls --json has always printed for a row.
func lsSourceType(item models.SkillItem) string {
	switch item.Kind {
	case models.InventorySymlink:
		return "local_symlink"
	case models.InventoryCommand:
		return "local_command"
	case models.InventoryUntracked:
		return "untracked"
	case models.InventoryUntrackedLink:
		return "symlink"
	default:
		return item.RepoType
	}
}
