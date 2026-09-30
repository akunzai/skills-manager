package cli

import (
	"errors"
	"os"
	"strings"

	"github.com/akunzai/skills-manager/internal/engine"
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

// flagPathOverrides reads cmd's parsed --config/--skills-dir/--cache-dir
// flags: empty unless the user explicitly set them. cmd's flag values live on
// the pflag.FlagSet built fresh for this execution by newRootCmd, so nothing
// here can carry over from a previous run.
func flagPathOverrides(cmd *cobra.Command) (configOverride, skillsDirOverride, cacheDirOverride string) {
	flags := cmd.Flags()
	if flags.Changed("config") {
		configOverride, _ = flags.GetString("config")
	}
	if flags.Changed("skills-dir") {
		skillsDirOverride, _ = flags.GetString("skills-dir")
	}
	if flags.Changed("cache-dir") {
		cacheDirOverride, _ = flags.GetString("cache-dir")
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
// only cmd's path-override flags and the working directory — never
// --project/--global. ResolveScope and add's scope prompt both go through
// this; the only difference between them is where isProject comes from.
func resolveScopeFor(cmd *cobra.Command, isProject bool) Scope {
	configOverride, skillsDirOverride, cacheDirOverride := flagPathOverrides(cmd)
	return resolveScope(isProject, workingDir(), configOverride, skillsDirOverride, cacheDirOverride)
}

// scopeFlagsOf is what a suggested command needs after its subcommand to act
// on s: the Scope flags the user passed, -p and any path override, so it
// reaches the same Config, skills directory and Cache. It follows the flags,
// not the shape of --skills-dir, and adds nothing the user did not pass.
func scopeFlagsOf(cmd *cobra.Command, s Scope) string {
	var flags strings.Builder
	if s.IsProject {
		flags.WriteString(" -p")
	}
	configOverride, skillsDirOverride, cacheDirOverride := flagPathOverrides(cmd)
	for _, override := range []struct{ flag, value string }{
		{"--config", configOverride},
		{"--skills-dir", skillsDirOverride},
		{"--cache-dir", cacheDirOverride},
	} {
		if override.value != "" {
			flags.WriteString(" " + override.flag + " " + shellWord(override.value))
		}
	}
	return flags.String()
}

// shellWord is path as one shell word: bare when nothing in it needs quoting,
// which keeps a suggested command readable inside the quotes around it.
// ':' and '\' stay bare too: a Windows path is literal inside those quotes,
// and quoting it again would end the suggestion at the path.
func shellWord(path string) string {
	tilded := models.ToTildePath(path)
	if strings.Trim(tilded, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._~-:\\") == "" && !strings.HasPrefix(tilded, "-") {
		return tilded
	}
	return shellQuotePath(path)
}

// withNext is reason followed by the skills command next names, if any,
// rendered to reach the Scope scopeFlags name.
func withNext(reason, next, scopeFlags string) string {
	if next == "" {
		return reason
	}
	return engine.NextCommand{Reason: reason, Command: next}.Render(scopeFlags)
}

// withScopeFlags is err with any skills command it names rendered to reach
// the Scope scopeFlags name, keeping whatever context wraps it.
func withScopeFlags(err error, scopeFlags string) error {
	next, ok := errors.AsType[engine.NextCommand](err)
	if !ok {
		return err
	}
	return errors.New(strings.Replace(err.Error(), next.Error(), next.Render(scopeFlags), 1))
}

// ResolveScope reads cmd's parsed --project/--global/--config/--skills-dir/
// --cache-dir flags and the working directory, and resolves them to a Scope.
// Call it once per command invocation, with the *cobra.Command RunE was
// handed — never a stashed reference from an earlier run.
func ResolveScope(cmd *cobra.Command) Scope {
	flags := cmd.Flags()
	isProject, _ := flags.GetBool("project")
	global, _ := flags.GetBool("global")
	if flags.Changed("global") && global {
		isProject = false
	}
	return resolveScopeFor(cmd, isProject)
}

// newRootCmd builds a fresh command tree: a new root Command, its own
// pflag.FlagSet for every persistent flag, and a fresh instance of every
// subcommand. Nothing here is a package-level variable, so no flag value or
// Changed bit can survive from one build to the next — each call starts from
// the declared defaults, exactly like a freshly started process. Execute
// calls this once per invocation; tests call it once per test.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "skills",
		Short:         "Skills manager for AI agents",
		Long:          `A fast, cross-platform standalone CLI to discover, install, update, and manage skills across AI agents (Claude Code, Codex, GitHub Copilot CLI, Antigravity CLI, etc.).`,
		Version:       updater.Version,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			selfUpdateNoticeCmd = cmd
			applyOutputStyle(cmd.OutOrStdout())
			applyErrorOutputStyle(cmd.ErrOrStderr())
			return nil
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			maybeNotifySelfUpdate(cmd)
			return nil
		},
	}

	root.PersistentFlags().Bool("no-mouse", false, "Disable mouse input in interactive lists")
	root.PersistentFlags().String("config", "", "Path to skills.json")
	root.PersistentFlags().String("skills-dir", "", "Path to skills directory")
	root.PersistentFlags().String("cache-dir", "", "Path to cache directory")
	root.PersistentFlags().BoolP("global", "g", true, "Manage global skills")
	root.PersistentFlags().BoolP("project", "p", false, "Manage current project skills")
	root.AddCommand(newLsCmd())
	root.AddCommand(newAddCmd())
	root.AddCommand(newRmCmd())
	root.AddCommand(newTrustCmd())
	root.AddCommand(newSyncCmd())
	root.AddCommand(newPruneCmd())
	root.AddCommand(newAdoptCmd())
	root.AddCommand(newOutdatedCmd())
	root.AddCommand(newUpdateCmd())
	root.AddCommand(newDiffCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newSelfUpdateCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newVersionCmd())
	root.AddCommand(newConfigCmd())
	root.AddCommand(newAgentsCmd())
	root.AddCommand(newGuideCmd())
	return root
}

// Execute builds a fresh command tree and runs it against the process's own
// args, stdin, stdout and stderr.
func Execute() error {
	return execute(newRootCmd())
}

// execute is Execute's core, taking the *cobra.Command to run so a test can
// hand it one already configured with SetArgs/SetOut/SetErr — the same
// tree-per-call guarantee newRootCmd gives Execute.
func execute(cmd *cobra.Command) error {
	selfUpdateNoticeCmd = nil
	err := cmd.Execute()
	// Cobra skips PersistentPostRunE when RunE returns. outdated, sync, and
	// doctor still finished; mention a Self-update without changing that error.
	if err != nil && selfUpdateNoticeCmd != nil {
		maybeNotifySelfUpdate(selfUpdateNoticeCmd)
	}
	return err
}
