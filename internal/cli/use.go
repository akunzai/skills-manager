package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/spf13/cobra"
)

func newUseCmd() *cobra.Command {
	var (
		flagSkill     string
		flagPath      string
		flagURL       string
		flagBranch    string
		flagTrustCert string
	)
	cmd := &cobra.Command{
		Use:   "use <source>",
		Short: "Print a remote Skill's SKILL.md without adding it",
		Long: `Print one Skill's SKILL.md from a remote Source that is not declared, as raw
bytes on stdout, so it can be piped into an Agent. Nothing is declared or
Materialized: Config, the Scope skills directory, and Agent directories are
left alone, and only the Cache is refreshed, as 'skills add --list' does.
Sources take the forms 'skills add' accepts: Git, Gist, and well-known.

Only SKILL.md is printed. A Skill whose instructions rely on its other files
(scripts/, references/) needs 'skills add' instead.

A signed Skill is verified first, and a Skill that does not verify is not
printed. A keyless Signer is named on stderr and not recorded.

Exits 0 when the SKILL.md is printed; 1 when the Skill's signature does not
verify or there is no SKILL.md (nothing is printed; the reason goes to
stderr); and 2 when the Source or the Sigstore trust root cannot be fetched,
or for a usage error, such as a Source with several Skills and no --skill.

Use reads no Scope, so -g, -p, --config, and --skills-dir have no effect.`,
		Example: "  skills use owner/repo --skill my-skill | claude\n  skills use gist:0123456789abcdef",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, source, err := engine.ClassifyAddKind(engine.AddSourceSpec{
				Positional: args[0],
				Path:       flagPath,
				Branch:     flagBranch,
				URL:        flagURL,
				TrustCert:  flagTrustCert,
			})
			refused := engine.IsWellKnownRefusal(err)
			if err != nil && !refused {
				return err
			}
			if kind != engine.AddSourceRemote && kind != engine.AddSourceWellKnown {
				cmd.SilenceUsage = true
				return fmt.Errorf("%s is not a remote Source; print a declared Skill with 'skills cat'", args[0])
			}
			// Past argument checks, every failure is a runtime problem.
			cmd.SilenceUsage = true
			if refused {
				return wellKnownRefusal(cmd, err)
			}
			spec := engine.RemoteIntakeSpec(kind, source, flagURL, flagBranch, flagPath)
			listHint := useListHint(args[0], flagURL, flagBranch, flagPath)
			// A Skill use will not print is its state, exit 1 without an
			// Error: prefix; a failure to read the Source is exit 2 (ADR-0002).
			blocked := func(state string) error {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s.\n", state)
				return exitError{message: state, code: 1}
			}
			// An empty Config: use reads no Scope, so no declared branch
			// applies. No progress is drawn, so stderr stays quiet on success.
			intake, err := engine.PrepareRemoteIntake(config.DefaultConfig(), spec, ResolveScope(cmd).CacheDir)
			if engine.IsWellKnownRefusal(err) {
				return wellKnownRefusal(cmd, err)
			}
			if err != nil {
				return err
			}
			for _, skipped := range intake.Skipped {
				fmt.Fprintf(cmd.ErrOrStderr(), "%sWarning: %s%s\n", colorYellow, skipped, colorReset)
			}
			if len(intake.Discovered) == 0 {
				return blocked(fmt.Sprintf("%s has no SKILL.md", spec.SourceKey))
			}
			name, subpath, err := useSkill(intake.Discovered, flagSkill, spec.SourceKey, listHint)
			if err != nil {
				return err
			}
			used, err := intake.Use(subpath, flagTrustCert)
			var notPrinted *engine.UseBlockedError
			if errors.As(err, &notPrinted) {
				return blocked(fmt.Sprintf("%s from %s is not printed: %s", name, spec.SourceKey, notPrinted.Reason))
			}
			if err != nil {
				return fmt.Errorf("%s: %w", spec.SourceKey, err)
			}
			printUseSigner(cmd.ErrOrStderr(), used)
			_, err = cmd.OutOrStdout().Write(used.Body)
			return err
		},
	}
	cmd.Flags().StringVarP(&flagSkill, "skill", "s", "", "Skill to print, when the Source has several")
	cmd.Flags().StringVar(&flagPath, "path", "", "Limit discovery to a relative path within the repository")
	cmd.Flags().StringVar(&flagURL, "url", "", "Custom Git clone URL")
	cmd.Flags().StringVar(&flagBranch, "branch", "", "Git branch or tag")
	cmd.Flags().StringVar(&flagTrustCert, "trust-cert", "", "PEM certificate the Skill's signature must chain to")
	return cmd
}

// useSkill is the one Skill use prints, by name and subpath: the Source's
// only Skill, or the one named. A choice the arguments do not settle is a
// usage error, exit 2.
func useSkill(discovered engine.DiscoveredSkills, name, sourceKey, listHint string) (string, string, error) {
	if name == "" {
		if len(discovered) > 1 {
			return "", "", fmt.Errorf("%s has %d Skills; choose one with --skill (run '%s' to see them)", sourceKey, len(discovered), listHint)
		}
		for only := range discovered {
			name = only
		}
	}
	paths, ok := discovered[name]
	switch {
	case !ok:
		return "", "", fmt.Errorf("no Skill %s in %s (run '%s' to see them)", name, sourceKey, listHint)
	case len(paths) > 1:
		return "", "", fmt.Errorf("%s has %d Skills named %s; narrow it with --path", sourceKey, len(paths), name)
	}
	return name, paths[0], nil
}

// useListHint is the 'skills add --list' that lists the Source use read,
// with the flags that chose it.
func useListHint(source, url, branch, path string) string {
	hint := "skills add " + source
	for _, flag := range []struct{ name, value string }{{"url", url}, {"branch", branch}, {"path", path}} {
		if flag.value != "" {
			hint += " --" + flag.name + " " + flag.value
		}
	}
	return hint + " --list"
}

// printUseSigner names the keyless identity that signed what use printed,
// since use verifies it without a Signer recorded to hold it to (ADR-0010).
func printUseSigner(w io.Writer, used engine.UsedSkill) {
	if used.Signer == nil {
		return
	}
	fmt.Fprintf(w, "Signed by %s (issuer %s); not recorded, since use declares nothing.\n", used.Signer.Identity, used.Signer.Issuer)
}
