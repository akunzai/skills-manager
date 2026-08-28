package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/spf13/cobra"
)

func newOutdatedCmd() *cobra.Command {
	var flagJSON bool
	cmd := &cobra.Command{
		Use:     "outdated",
		Aliases: []string{"check", "check-update"},
		Short:   "Inspect remote Source, shared Cache, and selected Scope freshness",
		Long:    "Inspect remote Source, shared Cache, and selected Scope freshness.\n\nExit codes: 0 current, 1 differences found, 2 check failed.",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			scope := ResolveScope()
			cfg, err := config.LoadConfig(scope.ConfigPath)
			if err != nil {
				return err
			}
			if len(cfg.Remote) == 0 {
				if flagJSON {
					fmt.Fprintln(cmd.OutOrStdout(), `{"repositories":[]}`)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "%sNo remote Sources configured in %s.%s\n", colorYellow, filepath.Base(scope.ConfigPath), colorReset)
				}
				return nil
			}
			report, err := engine.InspectFreshness(cfg, scope.SkillsDir, scope.CacheDir, engine.FreshnessOptions{ObserveRemote: true, ObserveScope: true, Workers: 8})
			if err != nil {
				return err
			}
			if flagJSON {
				data, marshalErr := json.MarshalIndent(report, "", "  ")
				if marshalErr != nil {
					return marshalErr
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
			} else {
				printOutdatedReport(cmd, report)
			}
			if !report.Fresh() {
				return exitError{message: "remote Source, Cache, or Scope is not current", code: 1}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&flagJSON, "json", false, "Output machine-readable JSON")
	return cmd
}

func printOutdatedReport(cmd *cobra.Command, report *engine.FreshnessSnapshot) {
	out := cmd.OutOrStdout()
	for _, repository := range report.Repositories {
		fmt.Fprintf(out, "%s%s%s  %sCache:%s %s", colorBold, repository.Source, colorReset, colorDim, colorReset, styledStatus(string(repository.RemoteStatus)))
		if repository.Error != "" {
			fmt.Fprintf(out, " %s(%s)%s", colorDim, repository.Error, colorReset)
		}
		fmt.Fprintln(out)
		for _, skill := range repository.Skills {
			note := ""
			if skill.Status == engine.SkillInSync && !skill.BaselineRecorded {
				note = fmt.Sprintf(" %s(baseline not recorded)%s", colorDim, colorReset)
			}
			fmt.Fprintf(out, "  %s%s%s %s: %s%s %s[%s]%s\n", colorDim, treeBranch, colorReset, skill.Name, styledStatus(string(skill.Status)), note, colorDim, models.ToTildePath(skill.ScopePath), colorReset)
		}
	}
	var hints []string
	for _, disposition := range report.DispositionKinds() {
		switch disposition {
		case engine.FreshnessUpdate:
			hints = append(hints, "run 'skills update'")
		case engine.FreshnessSync:
			hints = append(hints, "run 'skills sync'")
		case engine.FreshnessProtectDrift:
			hints = append(hints, "review local changes, then use 'skills sync --force' if intended")
		}
	}
	if len(hints) > 0 {
		fmt.Fprintf(out, "\nNext: %s.\n", strings.Join(hints, "; "))
	}
}

func humanStatus(status string) string {
	return strings.ToUpper(status[:1]) + strings.ReplaceAll(status[1:], "_", " ")
}

func styledStatus(status string) string {
	color := colorYellow
	switch status {
	case "up_to_date", string(engine.SkillInSync):
		color = colorGreen
	case string(engine.SkillError), string(engine.SkillMissing):
		color = colorRed
	}
	return colorBold + color + humanStatus(status) + colorReset
}
