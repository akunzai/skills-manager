package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and change skills configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, skillsDir, _ := GetEffectivePaths()
			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}
			scope := "Global"
			if models.IsProjectScope(skillsDir) {
				scope = "Project"
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Scope: %s\nConfig: %s\n", scope, models.ToTildePath(configPath))
			fmt.Fprintf(out, "Default agents: %s (%s)\n", displayList(cfg.Settings.DefaultAgents), models.ToTildePath(configPath))
			if len(cfg.Settings.Availability) == 0 {
				fmt.Fprintln(out, "Availability overrides: none")
			} else {
				names := make([]string, 0, len(cfg.Settings.Availability))
				for name := range cfg.Settings.Availability {
					names = append(names, name)
				}
				sort.Strings(names)
				fmt.Fprintln(out, "Availability overrides:")
				for _, name := range names {
					override := cfg.Settings.Availability[name]
					fmt.Fprintf(out, "  %s: include [%s]; exclude [%s]\n", name, strings.Join(override.Include, ", "), strings.Join(override.Exclude, ", "))
				}
			}
			return nil
		},
	}
	cmd.AddCommand(newConfigGetCmd(), newConfigSetCmd(), newConfigEditCmd())
	return cmd
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Read a configuration value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, _, _ := GetEffectivePaths()
			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}
			var value any
			switch {
			case args[0] == "defaultAgents":
				value = cfg.Settings.DefaultAgents
			case strings.HasPrefix(args[0], "availability."):
				value = cfg.Settings.Availability[strings.TrimPrefix(args[0], "availability.")]
			default:
				return fmt.Errorf("unknown config key %q", args[0])
			}
			data, err := json.Marshal(value)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return nil
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <values...>",
		Short: "Set a configuration value",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, skillsDir, _ := GetEffectivePaths()
			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}
			values := splitValues(args[1:])
			switch args[0] {
			case "defaultAgents":
				if len(values) == 0 {
					return fmt.Errorf("defaultAgents requires at least one agent")
				}
				normalized, err := validateAgentNames(values, skillsDir)
				if err != nil {
					return err
				}
				cfg.Settings.DefaultAgents = normalized
			default:
				return fmt.Errorf("unknown config key %q", args[0])
			}
			if err := config.SaveConfig(cfg, configPath); err != nil {
				return err
			}
			if args[0] == "defaultAgents" {
				for _, skill := range config.GetConfiguredSkillNames(cfg) {
					_, installed, err := configuredSkillSource(cfg, skill, skillsDir)
					if err != nil {
						return err
					}
					if installed {
						if err := engine.ApplyAvailability(skill, cfg, skillsDir); err != nil {
							return fmt.Errorf("saved %s but failed to apply availability for %s: %w", args[0], skill, err)
						}
					}
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Set %s in %s.\n", args[0], models.ToTildePath(configPath))
			return nil
		},
	}
}

func newConfigEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open the active configuration in an editor",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, _, _ := GetEffectivePaths()
			if _, err := os.Stat(configPath); os.IsNotExist(err) {
				if err := config.SaveConfig(config.DefaultConfig(), configPath); err != nil {
					return err
				}
			}
			editor := strings.TrimSpace(os.Getenv("VISUAL"))
			if editor == "" {
				editor = strings.TrimSpace(os.Getenv("EDITOR"))
			}
			if editor == "" {
				if runtime.GOOS == "windows" {
					editor = "notepad"
				} else {
					editor = "vi"
				}
			}
			process := editorCommand(editor, configPath)
			process.Stdin = cmd.InOrStdin()
			process.Stdout = cmd.OutOrStdout()
			process.Stderr = cmd.ErrOrStderr()
			return process.Run()
		},
	}
}

func editorCommand(editor, configPath string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		// On Windows, treat the configured value as an executable path. This
		// preserves paths containing spaces without attempting cmd.exe quoting.
		return exec.Command(editor, configPath)
	}
	// VISUAL and EDITOR conventionally allow arguments (for example,
	// "code --wait"). Pass the config path separately so the shell cannot
	// reinterpret it.
	return exec.Command("sh", "-c", editor+` "$1"`, "skills-config", configPath)
}

func validateAgentNames(values []string, skillsDir string) ([]string, error) {
	known := models.GetAgentsForSkillsDir(skillsDir)
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		agent := models.NormalizeAgentName(value)
		if models.IsUniversalAgent(agent, skillsDir) {
			return nil, fmt.Errorf("%s is automatically available and does not need an agent policy", agent)
		}
		if _, ok := known[agent]; !ok {
			return nil, fmt.Errorf("unknown agent %q for this scope", value)
		}
		if _, duplicate := seen[agent]; duplicate {
			continue
		}
		seen[agent] = struct{}{}
		normalized = append(normalized, agent)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func splitValues(values []string) []string {
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func displayList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}
