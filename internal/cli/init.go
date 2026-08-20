package cli

import (
	"fmt"
	"os"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	var flagForce bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new skills.json configuration file",
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, _, _ := GetEffectivePaths()

			if _, err := os.Stat(configPath); err == nil && !flagForce {
				return fmt.Errorf("config file already exists at %s (use --force to overwrite)", configPath)
			}

			cfg := config.DefaultConfig()
			if err := config.SaveConfig(cfg, configPath); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			fmt.Printf("%s✔ Initialized skills configuration at %s%s\n", colorGreen, configPath, colorReset)
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
			fmt.Printf("skills %s\n", RootCmd.Version)
		},
	}
}
