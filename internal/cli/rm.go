package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/akunzai/skills-manager/internal/tui"
	"github.com/spf13/cobra"
)

// rmIsTerminal and rmConfirm are seams over the terminal so a test can
// answer rm's confirmation.
var (
	rmIsTerminal = tui.IsTerminal
	rmConfirm    = func(prompt string) (bool, error) { return tui.PromptConfirm(prompt, false) }
)

func newRmCmd() *cobra.Command {
	var (
		flagYes bool
	)

	cmd := &cobra.Command{
		Use:     "rm [skills...]",
		Aliases: []string{"remove"},
		Short:   "Remove one or more skills",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Past flag parsing, every failure below is a runtime problem rather
			// than misuse, so reporting it with a usage dump would mislead.
			cmd.SilenceUsage = true
			out := cmd.OutOrStdout()
			scope := ResolveScope()
			configPath, skillsDir := scope.ConfigPath, scope.SkillsDir

			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}

			skillsToRemove := args

			if len(skillsToRemove) == 0 {
				if tui.IsTerminal() && !flagYes {
					inv, err := engine.LoadInventory(cfg, skillsDir)
					if err != nil {
						return err
					}
					allSkills := inv.SkillItems()
					if len(allSkills) == 0 {
						fmt.Fprintf(out, "%sNo skills installed or configured to remove.%s\n", colorYellow, colorReset)
						return nil
					}

					groups := make(map[string][]tui.SelectOption)
					for _, s := range allSkills {
						srcKey := s.Source
						groups[srcKey] = append(groups[srcKey], tui.SelectOption{
							Key:       s.Name,
							Title:     s.Name,
							Installed: s.IsInstalled,
							Selected:  false,
						})
					}

					chosen, err := tui.PromptGroupedMultiSelect("Select skills to remove:", groups)
					if err != nil {
						return err
					}
					if chosen == nil {
						fmt.Fprintf(out, "%sOperation cancelled.%s\n", colorYellow, colorReset)
						return nil
					}
					if len(chosen) == 0 {
						fmt.Fprintf(out, "%sNo skills selected. Aborted.%s\n", colorYellow, colorReset)
						return nil
					}
					skillsToRemove = chosen
				} else {
					cmd.SilenceUsage = false
					return fmt.Errorf("skill name(s) required")
				}
			}

			plan := engine.BuildRemovePlan(cfg, skillsDir, skillsToRemove)
			// A real directory Config does not declare is content Sync never
			// wrote, so it goes only once the user says so, and before
			// anything else is removed.
			if untracked := plan.UntrackedDirectories(); len(untracked) > 0 && !flagYes {
				subject := fmt.Sprintf("%d untracked directories", len(untracked))
				if len(untracked) == 1 {
					subject = "1 untracked directory"
				}
				if !rmIsTerminal() {
					return fmt.Errorf("refusing to remove %s without a terminal; rerun with --yes", subject)
				}
				confirmed, err := rmConfirm(fmt.Sprintf("Remove %s Config does not declare (%s)?", subject, strings.Join(untracked, ", ")))
				if err != nil {
					return err
				}
				if !confirmed {
					fmt.Fprintf(out, "%sOperation cancelled.%s\n", colorYellow, colorReset)
					return nil
				}
			}
			result, err := engine.ApplyRemovePlan(plan, cfg, configPath, skillsDir)
			if err != nil {
				return err
			}
			printRemoveResult(out, result)
			if result.StateWarning != "" {
				printScopeStateWarning(out, result.StateWarning, scopeFlagsOf(scope))
			}
			if result.StateError != "" {
				printScopeStateUnreadable(out, result.StateError)
			}
			if names := result.NotFullyRemoved(); len(names) > 0 {
				return exitError{message: "Skill removal did not complete: " + strings.Join(names, ", ") + " not fully removed", code: 2}
			}
			if result.StateError != "" {
				return exitError{message: "Baselines were not forgotten", code: 2}
			}

			fmt.Fprintf(out, "%sSkill removal complete.%s\n", colorGreen, colorReset)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip confirmation prompts")

	return cmd
}

func printRemoveResult(out io.Writer, result engine.RemoveResult) {
	for _, s := range result.Skills {
		fmt.Fprintf(out, "%sRemoving skill: %s%s%s...\n", colorCyan, colorBold, s.Name, colorReset)
		if s.RemovedFromConfig {
			fmt.Fprintf(out, "  %sRemoved from configuration.%s\n", colorGreen, colorReset)
		}
		if len(s.Unlinked) > 0 {
			agents := make([]string, len(s.Unlinked))
			for i, link := range s.Unlinked {
				agents[i] = link.Agent
			}
			fmt.Fprintf(out, "  %sUnlinked from: %s.%s\n", colorGreen, strings.Join(agents, ", "), colorReset)
		}
		for _, link := range s.FailedLinks {
			fmt.Fprintf(out, "  %sFailed to unlink from %s: %s: %s%s\n", colorRed, link.Agent, models.ToTildePath(link.Path), pathCause(link.Err), colorReset)
		}
		if s.CopyRemoved && s.MasterExisted {
			fmt.Fprintf(out, "  %sRemoved master directory: %s.%s\n", colorGreen, models.ToTildePath(s.CopyPath), colorReset)
		}
		if s.CopyKept {
			fmt.Fprintf(out, "  %sLeft %s in place: a local Skill only links to its Source, and this is a directory.%s\n", colorYellow, models.ToTildePath(s.CopyPath), colorReset)
		}
		if s.CopyErr != nil {
			fmt.Fprintf(out, "  %sFailed to remove master directory: %s: %s%s\n", colorRed, models.ToTildePath(s.CopyPath), pathCause(s.CopyErr), colorReset)
		}
		if s.BaselineErr != nil && !errors.Is(s.BaselineErr, engine.ErrNotRecorded) {
			fmt.Fprintf(out, "  %sFailed to forget the Baseline: %s%s\n", colorRed, s.BaselineErr, colorReset)
		}
	}
}

// pathCause is err without the path a *fs.PathError repeats, for a line that
// already names it.
func pathCause(err error) error {
	if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
		return pathErr.Err
	}
	return err
}
