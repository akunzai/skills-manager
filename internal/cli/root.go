package cli

import (
	"os"
	"path/filepath"
	"strings"

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

// IsProjectScope reports whether skillsDir is project-scoped rather than the
// global skills directory. It mirrors models.GetAgentsForSkillsDir's own test.
func IsProjectScope(skillsDir string) bool {
	if skillsDir == "" {
		return false
	}
	absSkills, err := filepath.Abs(skillsDir)
	if err != nil {
		return false
	}
	absGlobal, err := filepath.Abs(models.DefaultSkillsDir())
	if err != nil {
		return false
	}
	return filepath.Clean(absSkills) != filepath.Clean(absGlobal)
}

// StoreLocalSourcePath renders a local skill source for skills.json. Inside a
// project it returns a path relative to the project root, so the committed
// config resolves on a teammate's checkout instead of pointing at the author's
// absolute path. Anything outside the project, and all of global scope, uses
// ~/ prefix if inside the user's home directory, or the absolute path.
func StoreLocalSourcePath(absSource string, skillsDir string) string {
	if !IsProjectScope(skillsDir) {
		return models.ToTildePath(absSource)
	}
	projectRoot := models.GetProjectRootFromSkillsDir(skillsDir)
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return models.ToTildePath(absSource)
	}
	rel, err := filepath.Rel(absRoot, absSource)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return models.ToTildePath(absSource)
	}
	return filepath.ToSlash(rel)
}

// LocalSymlinkTarget returns what the skills-dir symlink should point at. A
// source inside the project is linked relatively (e.g. ../../my-skill) so the
// checkout survives being cloned elsewhere; anything else stays absolute.
func LocalSymlinkTarget(absSource string, skillsDir string) string {
	stored := StoreLocalSourcePath(absSource, skillsDir)
	if strings.HasPrefix(stored, "~") || filepath.IsAbs(stored) {
		return absSource
	}
	rel, err := filepath.Rel(skillsDir, absSource)
	if err != nil {
		return absSource
	}
	return rel
}

// ResolveLocalSourcePath turns a skills.json source back into a usable path,
// interpreting a relative one against the project root.
func ResolveLocalSourcePath(source string, skillsDir string) string {
	expanded := models.ExpandUser(source)
	if filepath.IsAbs(expanded) {
		return expanded
	}
	base := models.GetProjectRootFromSkillsDir(skillsDir)
	return filepath.Join(base, filepath.FromSlash(expanded))
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
		agentsConfigFile := filepath.Join(cwd, ".agents", "skills.json")
		rootConfigFile := filepath.Join(cwd, "skills.json")

		if _, err := os.Stat(agentsConfigFile); err == nil {
			configPath = agentsConfigFile
			skillsDir = filepath.Join(cwd, ".agents", "skills")
		} else if _, err := os.Stat(rootConfigFile); err == nil {
			configPath = rootConfigFile
			skillsDir = filepath.Join(cwd, "skills")
		} else {
			// Default for project mode is .agents/skills.json and .agents/skills
			configPath = agentsConfigFile
			skillsDir = filepath.Join(cwd, ".agents", "skills")
		}
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
}

func init() {
	RootCmd.PersistentFlags().StringVar(&flagConfigFile, "config", "", "Path to skills.json")
	RootCmd.PersistentFlags().StringVar(&flagSkillsDir, "skills-dir", "", "Path to skills directory")
	RootCmd.PersistentFlags().StringVar(&flagCacheDir, "cache-dir", "", "Path to cache directory")
	RootCmd.PersistentFlags().BoolVarP(&flagGlobal, "global", "g", true, "Manage global skills (default)")
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
}

func Execute() error {
	return RootCmd.Execute()
}
