package cli

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/tui"
	"github.com/spf13/cobra"
)

type pruneOptions struct {
	yes        bool
	dryRun     bool
	skillsOnly bool
	linksOnly  bool
}

const pruneMasterGroup = "Untracked master skills"

func newPruneCmd() *cobra.Command {
	var options pruneOptions
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove untracked skills and unconfigured managed links",
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			return runPrune(cmd, options)
		},
	}
	cmd.Flags().BoolVarP(&options.yes, "yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().BoolVar(&options.dryRun, "dry-run", false, "Show what would be removed without changing files")
	cmd.Flags().BoolVar(&options.skillsOnly, "skills-only", false, "Prune untracked master skills and their managed links")
	cmd.Flags().BoolVar(&options.linksOnly, "links-only", false, "Prune unconfigured managed links only")
	return cmd
}

func runPrune(cmd *cobra.Command, options pruneOptions) error {
	if options.skillsOnly && options.linksOnly {
		cmd.SilenceUsage = false
		return fmt.Errorf("--skills-only and --links-only cannot be used together")
	}
	configPath, skillsDir, _ := GetEffectivePaths()
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return err
	}

	includeSkills := !options.linksOnly
	includeConfiguredLinks := !options.skillsOnly
	plan, err := engine.BuildPrunePlan(cfg, skillsDir, includeSkills, includeConfiguredLinks)
	if err != nil {
		return fmt.Errorf("build prune plan: %w", err)
	}
	if len(plan.UntrackedSkills) == 0 && len(plan.Unconfigured) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "Nothing to prune.")
		return nil
	}

	if options.dryRun {
		printPrunePlan(cmd, plan)
		fmt.Fprintln(cmd.OutOrStdout(), "Dry run complete.")
		return nil
	}
	if !options.yes {
		if !tui.IsTerminal() {
			printPrunePlan(cmd, plan)
			return fmt.Errorf("refusing to prune without a terminal; rerun with --yes or --dry-run")
		}
		selected, err := promptPrunePlan(plan)
		if err != nil {
			return err
		}
		if selected == nil {
			fmt.Fprintln(cmd.OutOrStdout(), "Operation cancelled.")
			return nil
		}
		plan = selectedPrunePlan(plan, selected)
		if len(plan.UntrackedSkills) == 0 && len(plan.Unconfigured) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No items selected. Aborted.")
			return nil
		}
	} else {
		printPrunePlan(cmd, plan)
	}

	result, applyErr := engine.ApplyPrunePlan(plan, skillsDir)
	printPruneSummary(cmd, result)
	if applyErr != nil {
		for _, failure := range result.Failures {
			fmt.Fprintf(cmd.ErrOrStderr(), "Failed to prune %s: %v\n", failure.Path, failure.Err)
		}
		return fmt.Errorf("prune completed with failures: %w", applyErr)
	}
	return nil
}

func printPrunePlan(cmd *cobra.Command, plan engine.PrunePlan) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Prune plan: %d untracked master skills; %d unconfigured managed links.\n", len(plan.UntrackedSkills), len(plan.Unconfigured))
	agents := make(map[string]struct{})
	for _, link := range plan.Unconfigured {
		agents[link.Agent] = struct{}{}
	}
	if len(agents) > 0 {
		names := make([]string, 0, len(agents))
		for agent := range agents {
			names = append(names, agent)
		}
		sort.Strings(names)
		fmt.Fprintf(out, "Affected agents: %s\n", strings.Join(names, ", "))
	}
	for _, skill := range plan.UntrackedSkills {
		fmt.Fprintf(out, "  master skill: %s\n", skill)
	}
	for _, link := range plan.Unconfigured {
		fmt.Fprintf(out, "  managed link: %s (%s)\n", link.Path, link.Agent)
	}
}

func printPruneSummary(cmd *cobra.Command, result engine.PruneResult) {
	parts := make([]string, 0, 2)
	if n := len(result.RemovedSkills); n > 0 {
		label := "untracked master skills"
		if n == 1 {
			label = "untracked master skill"
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, label))
	}
	if n := len(result.RemovedLinks); n > 0 {
		label := "unconfigured agent links"
		if n == 1 {
			label = "unconfigured agent link"
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, label))
	}
	if len(parts) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Pruned %s.\n", strings.Join(parts, " and "))
	}
	if n := len(result.SkippedLinks); n > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Skipped %d changed or missing managed links.\n", n)
	}
	if n := len(result.Failures); n > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Failed to prune %d paths.\n", n)
	}
	for _, skill := range result.RemovedSkills {
		fmt.Fprintf(cmd.OutOrStdout(), "  Removed master skill: %s\n", skill)
	}
	for _, link := range result.RemovedLinks {
		fmt.Fprintf(cmd.OutOrStdout(), "  Removed managed link: %s\n", link.Path)
	}
	for _, link := range result.SkippedLinks {
		fmt.Fprintf(cmd.OutOrStdout(), "  Skipped managed link: %s\n", link.Path)
	}
}

func promptPrunePlan(plan engine.PrunePlan) ([]string, error) {
	groups := make(tui.GroupedItems)
	masterKeys := make(map[string]string, len(plan.UntrackedSkills))
	for _, skill := range plan.UntrackedSkills {
		key := pruneMasterKey(skill)
		masterKeys[skill] = key
		linked := 0
		for _, link := range plan.Unconfigured {
			if filepath.Base(link.Path) == skill {
				linked++
			}
		}
		extra := "no managed links"
		if linked == 1 {
			extra = "also removes 1 managed link"
		} else if linked > 1 {
			extra = fmt.Sprintf("also removes %d managed links", linked)
		}
		groups[pruneMasterGroup] = append(groups[pruneMasterGroup], tui.SelectOption{
			Key: key, Title: skill, Extra: extra,
		})
	}
	for _, link := range plan.Unconfigured {
		groups["Agent: "+link.Agent] = append(groups["Agent: "+link.Agent], tui.SelectOption{
			Key:       pruneLinkKey(link.Path),
			Title:     filepath.Base(link.Path),
			Extra:     link.Path,
			DependsOn: masterKeys[filepath.Base(link.Path)],
		})
	}
	return tui.PromptOrderedGroupedMultiSelect("Select items to prune:", groups, []string{pruneMasterGroup})
}

func selectedPrunePlan(plan engine.PrunePlan, selected []string) engine.PrunePlan {
	selectedSet := make(map[string]bool, len(selected))
	for _, key := range selected {
		selectedSet[key] = true
	}
	result := engine.PrunePlan{}
	selectedMasters := make(map[string]bool)
	for _, skill := range plan.UntrackedSkills {
		if selectedSet[pruneMasterKey(skill)] {
			result.UntrackedSkills = append(result.UntrackedSkills, skill)
			selectedMasters[skill] = true
		}
	}
	for _, link := range plan.Unconfigured {
		if selectedSet[pruneLinkKey(link.Path)] || selectedMasters[filepath.Base(link.Path)] {
			result.Unconfigured = append(result.Unconfigured, link)
		}
	}
	return result
}

func pruneMasterKey(skill string) string {
	return "master:" + skill
}

func pruneLinkKey(path string) string {
	return "link:" + path
}
