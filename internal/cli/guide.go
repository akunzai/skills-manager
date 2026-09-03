package cli

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/spf13/cobra"
)

//go:embed embedded_skill.md
var embeddedGuideSkill string

func newGuideCmd() *cobra.Command {
	var flagInstall bool

	cmd := &cobra.Command{
		Use:   "guide",
		Short: "Print or install the Skills Manager guide for AI agents",
		Long: `Print the Skills Manager AI agent guide to stdout, or install it as a managed skill into the current Scope.

When invoked without flags, prints the complete SKILL.md guide in Markdown.
With --install, materializes the skill to the Scope skills directory, registers it in skills.json, and applies Agent Availability.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			if !flagInstall {
				_, err := fmt.Fprint(cmd.OutOrStdout(), embeddedGuideSkill)
				return err
			}

			scope := ResolveScope()
			skillDir := filepath.Join(scope.SkillsDir, "skills-manager")
			if err := os.MkdirAll(skillDir, 0o755); err != nil {
				return fmt.Errorf("create skill directory %s: %w", models.ToTildePath(skillDir), err)
			}

			skillMdPath := filepath.Join(skillDir, "SKILL.md")
			if err := os.WriteFile(skillMdPath, []byte(embeddedGuideSkill), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", models.ToTildePath(skillMdPath), err)
			}

			if err := os.MkdirAll(filepath.Dir(scope.ConfigPath), 0o755); err != nil {
				return fmt.Errorf("create config directory: %w", err)
			}

			cfg, err := config.LoadConfig(scope.ConfigPath)
			if err != nil {
				return fmt.Errorf("load config %s: %w", models.ToTildePath(scope.ConfigPath), err)
			}

			installCmd := "skills guide --install"
			if scope.IsProject {
				installCmd = "skills guide --install --project"
			}

			if cfg.Local == nil {
				cfg.Local = make(map[string]config.LocalEntry)
			}
			cfg.Local["skills-manager"] = config.LocalEntry{
				Type:        "command",
				Command:     installCmd,
				Description: "Skills Manager CLI guide for AI coding agents",
			}

			if err := config.SaveConfig(cfg, scope.ConfigPath); err != nil {
				return fmt.Errorf("save config %s: %w", models.ToTildePath(scope.ConfigPath), err)
			}

			availability := engine.NewAvailability(cfg, scope.SkillsDir)
			if err := availability.Apply("skills-manager"); err != nil {
				return fmt.Errorf("apply availability for skills-manager: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%sInstalled skills-manager to %s and configured agent availability.%s\n", colorGreen, models.ToTildePath(skillMdPath), colorReset)
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagInstall, "install", false, "Install skills-manager as a managed skill in the current Scope")
	return cmd
}
