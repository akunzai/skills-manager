package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/spf13/cobra"
)

func newTrustCmd() *cobra.Command {
	var flagYes, flagRevoke bool

	cmd := &cobra.Command{
		Use:   "trust <skills...>",
		Short: "Trust a skill's current content although its signature does not verify",
		Long: `Trust a skill's current content although its signature does not verify.

The content is recorded in Config as the skill's Cache tree id, so the trust
is reviewable and holds only for that content: once the Source changes it, the
skill is verified again. Run 'skills sync' to apply it. 'skills ls' marks a
skill applied this way as unverified.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			out := cmd.OutOrStdout()
			p := newPrompter(cmd)
			scope := ResolveScope(cmd)
			flags := scopeFlagsOf(cmd, scope)
			cfg, err := config.LoadConfig(scope.ConfigPath)
			if err != nil {
				return err
			}

			if flagRevoke {
				revoked, err := engine.RevokeTrust(cfg, scope.ConfigPath, args)
				if err != nil {
					return err
				}
				if len(revoked) > 0 {
					fmt.Fprintf(out, "%sRevoked trust in %s. Run 'skills sync%s' to verify %s again.%s\n", colorGreen, strings.Join(revoked, ", "), flags, pronoun(len(revoked)), colorReset)
				}
				if missing := slices.DeleteFunc(slices.Clone(args), func(name string) bool { return slices.Contains(revoked, name) }); len(missing) > 0 {
					return exitError{message: "no trusted content for " + strings.Join(missing, ", "), code: 2}
				}
				return nil
			}

			items, err := engine.PlanTrust(cfg, scope.ConfigPath, scope.SkillsDir, scope.CacheDir, args)
			if err != nil {
				return err
			}
			var trustable []engine.TrustItem
			var refused []string
			for _, item := range items {
				if item.Refused != "" {
					fmt.Fprintf(out, "%sCannot trust %s: %s%s\n", colorYellow, item.Name, withNext(item.Refused, item.Next, flags), colorReset)
					refused = append(refused, item.Name)
					continue
				}
				fmt.Fprintf(out, "%s  %s\n", item.Name, item.Reason)
				fmt.Fprintf(out, "%s  content %s from %s%s\n", colorDim, item.Tree, item.Source, colorReset)
				trustable = append(trustable, item)
			}
			if len(trustable) > 0 && !flagYes {
				if !p.Interactive() {
					return fmt.Errorf("refusing to trust unverified content without a terminal; rerun with --yes")
				}
				confirmed, err := p.Confirm(fmt.Sprintf("Trust this content of %s although its signature does not verify?", countOf(len(trustable), "skill")), false)
				if err != nil {
					return err
				}
				if !confirmed {
					fmt.Fprintf(out, "%sOperation cancelled.%s\n", colorYellow, colorReset)
					return nil
				}
			}
			if err := engine.ApplyTrust(cfg, scope.ConfigPath, trustable); err != nil {
				return err
			}
			if len(trustable) > 0 {
				fmt.Fprintf(out, "%sTrusted %s. Run 'skills sync%s' to apply %s.%s\n", colorGreen, countOf(len(trustable), "skill"), flags, pronoun(len(trustable)), colorReset)
			}
			if len(refused) > 0 {
				return exitError{message: "could not trust " + strings.Join(refused, ", "), code: 2}
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip the confirmation prompt")
	cmd.Flags().BoolVar(&flagRevoke, "revoke", false, "Remove the trust, so the skill's signature must verify again")

	return cmd
}

func pronoun(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}
