package cli

import (
	"os"
	"path/filepath"

	"github.com/akunzai/skills-manager/internal/models"
	"github.com/akunzai/skills-manager/internal/updater"
	"github.com/spf13/cobra"
)

var (
	flagConfigFile string
	flagSkillsDir  string
	flagCacheDir   string
	flagGlobal     bool
	flagProject    bool
)

func getProjectPaths(cwd string) (configPath, skillsDir string) {
	agentsConfigFile := filepath.Join(cwd, ".agents", "skills.json")
	rootConfigFile := filepath.Join(cwd, "skills.json")
	if _, err := os.Stat(agentsConfigFile); err == nil {
		return agentsConfigFile, filepath.Join(cwd, ".agents", "skills")
	}
	if _, err := os.Stat(rootConfigFile); err == nil {
		return rootConfigFile, filepath.Join(cwd, "skills")
	}
	return agentsConfigFile, filepath.Join(cwd, ".agents", "skills")
}

func GetEffectivePaths() (configPath string, skillsDir string, cacheDir string) {
	cacheDir = models.DefaultCacheDir()

	isProject := flagProject
	if RootCmd.PersistentFlags().Changed("global") && flagGlobal {
		isProject = false
	}

	if isProject {
		// Project mode
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "."
		}
		configPath, skillsDir = getProjectPaths(cwd)
	} else {
		// Global mode (default)
		configPath = models.DefaultConfigFile()
		skillsDir = models.DefaultSkillsDir()
	}

	if RootCmd.PersistentFlags().Changed("config") && flagConfigFile != "" {
		configPath = models.ExpandUser(flagConfigFile)
	}
	if RootCmd.PersistentFlags().Changed("skills-dir") && flagSkillsDir != "" {
		skillsDir = models.ExpandUser(flagSkillsDir)
	}
	if RootCmd.PersistentFlags().Changed("cache-dir") && flagCacheDir != "" {
		cacheDir = models.ExpandUser(flagCacheDir)
	}

	return configPath, skillsDir, cacheDir
}

var RootCmd = &cobra.Command{
	Use:     "skills",
	Short:   "Skills manager for AI coding agents",
	Long:    `A fast, cross-platform standalone CLI to discover, install, update, and manage skills across AI coding agents (Claude Code, Codex, GitHub Copilot CLI, Antigravity CLI, etc.).`,
	Version: updater.Version,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		applyOutputStyle(cmd.OutOrStdout())
		applyErrorOutputStyle(cmd.ErrOrStderr())
		return nil
	},
}

func init() {
	RootCmd.PersistentFlags().StringVar(&flagConfigFile, "config", "", "Path to skills.json")
	RootCmd.PersistentFlags().StringVar(&flagSkillsDir, "skills-dir", "", "Path to skills directory")
	RootCmd.PersistentFlags().StringVar(&flagCacheDir, "cache-dir", "", "Path to cache directory")
	RootCmd.PersistentFlags().BoolVarP(&flagGlobal, "global", "g", true, "Manage global skills")
	RootCmd.PersistentFlags().BoolVarP(&flagProject, "project", "p", false, "Manage current project skills")
	RootCmd.AddCommand(newLsCmd())
	RootCmd.AddCommand(newAddCmd())
	RootCmd.AddCommand(newRmCmd())
	RootCmd.AddCommand(newSyncCmd())
	RootCmd.AddCommand(newPruneCmd())
	RootCmd.AddCommand(newOutdatedCmd())
	RootCmd.AddCommand(newUpdateCmd())
	RootCmd.AddCommand(newDoctorCmd())
	RootCmd.AddCommand(newSelfUpdateCmd())
	RootCmd.AddCommand(newInitCmd())
	RootCmd.AddCommand(newVersionCmd())
	RootCmd.AddCommand(newConfigCmd())
	RootCmd.AddCommand(newAgentsCmd())
}

func Execute() error {
	return RootCmd.Execute()
}
