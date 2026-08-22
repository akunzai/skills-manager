package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/spf13/cobra"
)

func newAgentsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agents [skill] [include|exclude|reset|follow-defaults] [agents...]",
		Short: "Inspect and change skill availability",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, skillsDir, _ := GetEffectivePaths()
			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				return printAllAvailability(cmd, cfg)
			}
			skill := args[0]
			source, installed, err := configuredSkillSource(cfg, skill, skillsDir)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				return printSkillAvailability(cmd, cfg, skill, source, skillsDir)
			}

			action := args[1]
			agents := splitValues(args[2:])
			switch action {
			case "include", "exclude", "reset":
				if len(agents) == 0 {
					return fmt.Errorf("%s requires at least one agent", action)
				}
				agents, err = validateAgentNames(agents, skillsDir)
				if err != nil {
					return err
				}
				if action == "include" {
					err = config.IncludeSkillAgents(cfg, skill, agents...)
				} else if action == "exclude" {
					err = config.ExcludeSkillAgents(cfg, skill, agents...)
				} else {
					err = config.ResetSkillAgents(cfg, skill, agents...)
				}
			case "follow-defaults":
				if len(agents) > 0 {
					return fmt.Errorf("follow-defaults does not accept agents")
				}
				config.FollowDefaults(cfg, skill)
			default:
				return fmt.Errorf("unknown availability action %q", action)
			}
			if err != nil {
				return err
			}
			if err := config.SaveConfig(cfg, configPath); err != nil {
				return err
			}
			if installed {
				if err := engine.ReconcileAgentSymlinks(skill, source, cfg, skillsDir); err != nil {
					return fmt.Errorf("saved policy but failed to reconcile %s: %w", skill, err)
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated availability for %s.\n", skill)
			return printSkillAvailability(cmd, cfg, skill, source, skillsDir)
		},
	}
}

func configuredSkillSource(cfg *config.Config, skill, skillsDir string) (source string, installed bool, err error) {
	category, source, found := config.FindSkillSource(cfg, skill)
	if !found {
		return "", false, fmt.Errorf("skill %q is not configured", skill)
	}
	if category == "local" {
		source = "local"
	}
	_, statErr := os.Lstat(filepath.Join(skillsDir, skill))
	return source, statErr == nil, nil
}

func printAllAvailability(cmd *cobra.Command, cfg *config.Config) error {
	names := config.GetConfiguredSkillNames(cfg)
	if len(names) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No configured skills.")
		return nil
	}
	for _, name := range names {
		override, customized := cfg.Settings.Availability[name]
		if !customized {
			fmt.Fprintf(cmd.OutOrStdout(), "%s: follow defaults\n", name)
			continue
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s: include [%s]; exclude [%s]\n", name, strings.Join(override.Include, ", "), strings.Join(override.Exclude, ", "))
	}
	return nil
}

func printSkillAvailability(cmd *cobra.Command, cfg *config.Config, skill, source, skillsDir string) error {
	override, customized := cfg.Settings.Availability[skill]
	policy := "follow defaults"
	if customized {
		policy = fmt.Sprintf("include [%s]; exclude [%s]", strings.Join(override.Include, ", "), strings.Join(override.Exclude, ", "))
	}
	effective := engine.GetTargetAgentsForSkill(skill, source, cfg, skillsDir)
	sort.Strings(effective)
	fmt.Fprintf(cmd.OutOrStdout(), "Skill: %s\nPolicy: %s\nLinked by policy: %s\nUniversal agents: automatically available\n", skill, policy, displayList(effective))
	return nil
}
