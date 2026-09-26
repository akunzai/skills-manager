package cli

import (
	"fmt"
	"io"
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
			scope := ResolveScope()
			configPath, skillsDir := scope.ConfigPath, scope.SkillsDir
			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}
			availability := engine.NewAvailability(cfg, skillsDir)
			if len(args) == 0 {
				return printAllAvailability(cmd, cfg)
			}
			skill := args[0]
			source, err := configuredSkillSource(cfg, skill)
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
				agents, err = availability.ValidateManagedAgents(agents)
				if err != nil {
					return err
				}
				if action == "include" {
					err = availability.Include(skill, agents...)
				} else if action == "exclude" {
					err = availability.Exclude(skill, agents...)
				} else {
					err = availability.Reset(skill, agents...)
				}
			case "follow-defaults":
				if len(agents) > 0 {
					return fmt.Errorf("follow-defaults does not accept agents")
				}
				availability.FollowDefaults(skill)
			default:
				return fmt.Errorf("unknown availability action %q", action)
			}
			if err != nil {
				return err
			}
			// Past here every failure is a runtime problem, not misuse.
			cmd.SilenceUsage = true
			if err := config.SaveConfig(cfg, configPath); err != nil {
				return err
			}
			outcomes := availability.Reconcile(skill)
			fmt.Fprintf(cmd.OutOrStdout(), "Updated availability for %s.\n", skill)
			if err := printSkillAvailability(cmd, cfg, skill, source, skillsDir); err != nil {
				return err
			}
			return reportReconciled(cmd.OutOrStdout(), outcomes, scopeFlagsOf(scope))
		},
	}
}

func configuredSkillSource(cfg *config.Config, skill string) (string, error) {
	kind, source, found := config.FindSkillSource(cfg, skill)
	if !found {
		return "", fmt.Errorf("skill %q is not configured", skill)
	}
	if kind != config.SkillRemote {
		source = "local"
	}
	return source, nil
}

// reportReconciled says how applying Availability after a policy change went,
// in Sync's words: each Skill that failed, the copies once, and Doctor for a
// path Availability refuses. The policy is saved either way; a Skill left
// unapplied is work that failed (ADR-0002).
func reportReconciled(out io.Writer, outcomes []engine.AvailabilityOutcome, scopeFlag string) error {
	copied, failed, refused := 0, 0, false
	for _, outcome := range outcomes {
		copied += len(outcome.Copied)
		if outcome.Err != nil {
			failed++
			refused = refused || outcome.Refused
			fmt.Fprintf(out, "  %sFailed to apply availability for %s: %s%s\n", colorRed, outcome.Skill, outcome.Err, colorReset)
		}
	}
	if copied > 0 {
		fmt.Fprintln(out, "\n"+copiedAvailabilityNotice(copied, "", scopeFlag))
	}
	if refused {
		fmt.Fprintf(out, "Next: run 'skills doctor%s --fix'.\n", scopeFlag)
	}
	if failed > 0 {
		return exitError{message: "Availability not applied for " + countOf(failed, "skill"), code: 2}
	}
	return nil
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
	availability := engine.NewAvailability(cfg, skillsDir)
	fmt.Fprintf(cmd.OutOrStdout(), "Skill: %s\nPolicy: %s\nLinked by policy: %s\nAutomatically available: %s\n", skill, policy, displayList(availability.ManagedAgents(skill)), displayList(availability.AutomaticallyAvailable()))
	return nil
}
