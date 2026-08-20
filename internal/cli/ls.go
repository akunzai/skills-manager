package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/spf13/cobra"
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
								break
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
						"path":       installedP,
						"scope":      scope,
						"agents":     s.LinkedAgents,
						"source":     s.Source,
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

			fmt.Printf("\n%s%sSkills (%d total):%s\n\n", colorBold, colorCyan, len(skills), colorReset)
			fmt.Printf("%s%-32s %-38s %-12s %s%s\n", colorBold, "NAME", "SOURCE", "AGENTS", "STATUS", colorReset)
			fmt.Println(strings.Repeat("─", 94))

			for _, s := range skills {
				var statusBadges []string
				if s.IsInstalled {
					if s.IsValidSkill {
						statusBadges = append(statusBadges, fmt.Sprintf("%sInstalled%s", colorGreen, colorReset))
					} else {
						statusBadges = append(statusBadges, fmt.Sprintf("%sInvalid (No SKILL.md)%s", colorRed, colorReset))
					}
				} else {
					statusBadges = append(statusBadges, fmt.Sprintf("%sMissing%s", colorYellow, colorReset))
				}

				var sourceDisplay string
				if strings.HasPrefix(s.SourceType, "local_") {
					rawSrc := fmt.Sprintf("[local] %s", s.Source)
					if len(rawSrc) > 38 {
						rawSrc = rawSrc[:35] + "..."
					}
					cleanRest := rawSrc[8:]
					sourceDisplay = fmt.Sprintf("%s[local]%s %-30s", colorDim, colorReset, cleanRest)
				} else if s.SourceType == "symlink" {
					rawSrc := fmt.Sprintf("[symlink] %s", s.Source)
					if len(rawSrc) > 38 {
						rawSrc = rawSrc[:35] + "..."
					}
					cleanRest := rawSrc[10:]
					sourceDisplay = fmt.Sprintf("%s[symlink]%s %-28s", colorDim, colorReset, cleanRest)
				} else if s.SourceType == "untracked" {
					sourceDisplay = fmt.Sprintf("%s%-38s%s", colorYellow, "[untracked]", colorReset)
				} else {
					rawSrc := s.Source
					if len(rawSrc) > 38 {
						rawSrc = rawSrc[:35] + "..."
					}
					sourceDisplay = fmt.Sprintf("%-38s", rawSrc)
				}

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
					if len(targetList) == 1 {
						rawTargets = targetList[0]
					} else if len(targetList) == 2 && len(targetList[0])+len(targetList[1])+2 <= 12 {
						rawTargets = strings.Join(targetList, ", ")
					} else {
						rawTargets = fmt.Sprintf("%s (+%d)", targetList[0], len(targetList)-1)
					}
				}
				var agentsDisplay string
				if len(targetList) > 0 {
					agentsDisplay = fmt.Sprintf("%-12s", rawTargets)
				} else {
					agentsDisplay = fmt.Sprintf("%s%-12s%s", colorDim, rawTargets, colorReset)
				}

				nameDisplay := fmt.Sprintf("%s%-32s%s", colorBold, s.Name, colorReset)
				statusStr := strings.Join(statusBadges, " ")
				fmt.Printf("%s %s %s %s\n", nameDisplay, sourceDisplay, agentsDisplay, statusStr)
			}

			fmt.Println(strings.Repeat("─", 94) + "\n")
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output machine-readable JSON")
	cmd.Flags().StringVarP(&flagAgent, "agent", "a", "", "Filter by target agent name")
	cmd.Flags().StringVarP(&flagSource, "source", "s", "", "Filter skills by source repository or type")

	return cmd
}
