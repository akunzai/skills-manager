package cli

import (
	"fmt"
	"os"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	var flagForce bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new skills.json configuration file",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Past flag parsing, every failure below is a runtime problem rather
			// than misuse, so reporting it with a usage dump would mislead.
			cmd.SilenceUsage = true
			configPath, _, _ := GetEffectivePaths()

			if _, err := os.Stat(configPath); err == nil && !flagForce {
				return fmt.Errorf("config file already exists at %s (use --force to overwrite)", models.ToTildePath(configPath))
			}

			cfg := config.DefaultConfig()
			if err := config.SaveConfig(cfg, configPath); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			fmt.Printf("%sInitialized skills configuration at %s.%s\n", colorGreen, models.ToTildePath(configPath), colorReset)
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagForce, "force", false, "Overwrite existing configuration file")

	return cmd
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print skills manager version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "skills-manager %s\n", RootCmd.Version)
		},
	}
}
