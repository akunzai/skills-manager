package cli

import (
	"errors"
	"os"

	"github.com/akunzai/skills-manager/internal/models"
	"github.com/akunzai/skills-manager/internal/updater"
	"github.com/spf13/cobra"
)

type exitError struct {
	message string
	code    int
}

func (err exitError) Error() string { return err.message }
func (err exitError) ExitCode() int { return err.code }

func ExitCode(err error) int {
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return 2
}

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

// Scope is the resolved Global or Project configuration for one command
// invocation: its Config path, skills directory, and git Cache directory.
// Every command decides its Scope once, at the top of RunE via ResolveScope;
// nothing past that point re-derives it from flags. IsProject is the flag
// choice, not the skills-dir path shape.
type Scope = models.Scope

// resolveScope is ResolveScope's pure core: given whether this invocation is
// Project-scoped, the working directory, and any explicit path overrides, it
// derives a Scope.
func resolveScope(isProject bool, cwd, configOverride, skillsDirOverride, cacheDirOverride string) Scope {
	return models.ResolveScope(isProject, cwd, configOverride, skillsDirOverride, cacheDirOverride)
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
	RootCmd.AddCommand(newGuideCmd())
}

func Execute() error {
	RootCmd.SilenceErrors = true
	return RootCmd.Execute()
}
