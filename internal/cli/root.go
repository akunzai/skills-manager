package cli

import (
	"os"
	"path/filepath"

	"github.com/akunzai/skills-manager/internal/models"
	"github.com/akunzai/skills-manager/internal/updater"
	"github.com/spf13/cobra"
)

// Cobra-owned: bound to --config/--skills-dir/--cache-dir/--global/--project
// in init() below. Never assign to these directly outside flag parsing — a
// caller that already knows its Scope (an interactive prompt, for instance)
// must call resolveScopeFor or resolveScope instead. add's scope prompt used
// to write here, which is why cli_test.go once needed manual resets of
// flagProject/flagGlobal between tests to avoid one test's prompt answer
// leaking into the next.
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

// Scope is the resolved Global or Project configuration for one command
// invocation: its Config path, skills directory, and git Cache directory.
// Every command decides its Scope once, at the top of RunE; nothing past
// that point re-derives it from flags. SkillsDir alone is what
// models.IsProjectScope/ScopeRoot need to derive the Scope root — the
// boundary portable local Source paths and prune must not escape — so that
// aspect of the CONTEXT.md "Scope" definition is not duplicated here.
type Scope struct {
	ConfigPath string
	SkillsDir  string
	CacheDir   string
	IsProject  bool
}

// resolveScope is ResolveScope's pure core: given whether this invocation is
// Project-scoped, the working directory, and any explicit path overrides, it
// derives a Scope. It reads no package state and writes none, so a caller
// that already knows its Scope — the add prompt, once the user has picked
// one — can call it directly instead of going through mutable flag vars.
// An empty override leaves that path at its Scope default.
func resolveScope(isProject bool, cwd, configOverride, skillsDirOverride, cacheDirOverride string) Scope {
	cacheDir := models.DefaultCacheDir()
	var configPath, skillsDir string
	if isProject {
		configPath, skillsDir = getProjectPaths(cwd)
	} else {
		configPath = models.DefaultConfigFile()
		skillsDir = models.DefaultSkillsDir()
	}

	if configOverride != "" {
		configPath = models.ExpandUser(configOverride)
	}
	if skillsDirOverride != "" {
		skillsDir = models.ExpandUser(skillsDirOverride)
	}
	if cacheDirOverride != "" {
		cacheDir = models.ExpandUser(cacheDirOverride)
	}

	return Scope{ConfigPath: configPath, SkillsDir: skillsDir, CacheDir: cacheDir, IsProject: isProject}
}

// flagPathOverrides reads the parsed --config/--skills-dir/--cache-dir flags:
// empty unless the user explicitly set them.
func flagPathOverrides() (configOverride, skillsDirOverride, cacheDirOverride string) {
	if RootCmd.PersistentFlags().Changed("config") {
		configOverride = flagConfigFile
	}
	if RootCmd.PersistentFlags().Changed("skills-dir") {
		skillsDirOverride = flagSkillsDir
	}
	if RootCmd.PersistentFlags().Changed("cache-dir") {
		cacheDirOverride = flagCacheDir
	}
	return configOverride, skillsDirOverride, cacheDirOverride
}

func workingDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

// resolveScopeFor derives a Scope for an explicitly known isProject, reading
// only the path-override flags and the working directory — never
// --project/--global. ResolveScope and add's scope prompt both go through
// this; the only difference between them is where isProject comes from.
func resolveScopeFor(isProject bool) Scope {
	configOverride, skillsDirOverride, cacheDirOverride := flagPathOverrides()
	return resolveScope(isProject, workingDir(), configOverride, skillsDirOverride, cacheDirOverride)
}

// ResolveScope reads the parsed --project/--global/--config/--skills-dir/
// --cache-dir flags and the working directory, and resolves them to a Scope.
// Call it once per command invocation.
func ResolveScope() Scope {
	isProject := flagProject
	if RootCmd.PersistentFlags().Changed("global") && flagGlobal {
		isProject = false
	}
	return resolveScopeFor(isProject)
}

// GetEffectivePaths is ResolveScope's Scope unpacked into its three paths,
// for callers that only need the paths.
func GetEffectivePaths() (configPath string, skillsDir string, cacheDir string) {
	scope := ResolveScope()
	return scope.ConfigPath, scope.SkillsDir, scope.CacheDir
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
