package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/spf13/cobra"
)

func newDiffCmd() *cobra.Command {
	var (
		flagFetch bool
		flagStat  bool
	)

	cmd := &cobra.Command{
		Use:   "diff <skill>",
		Short: "Show upstream and local changes of a remote skill",
		Long: `Show what changed for a remote skill before updating it. Upstream runs from
the commit its Scope copy was applied from to the Cache; Local runs from that
applied content to the copy on the Scope skills directory. Without a recorded
baseline the two cannot be told apart, so only Upstream is shown, comparing the
Scope copy with the Cache.

diff reads the existing Cache without network access. --fetch first refreshes
the skill's Source into the Cache, as 'skills update' does, without syncing.

Exits 0 when there are no differences, 1 when there are, and 2 when the diff
could not be completed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Past argument parsing, every failure is a runtime problem.
			cmd.SilenceUsage = true
			name := args[0]
			scope := ResolveScope()
			cfg, err := config.LoadConfig(scope.ConfigPath)
			if err != nil {
				return err
			}
			if flagFetch {
				if err := fetchSkillSource(cmd, cfg, name, scope.CacheDir); err != nil {
					return err
				}
			}
			d, err := engine.DiffSkill(cfg, name, scope.SkillsDir, scope.CacheDir)
			if err != nil {
				return err
			}
			printSkillDiff(cmd.OutOrStdout(), d, flagStat, scopeFlagOf(scope))
			if !d.Empty() {
				return exitError{message: fmt.Sprintf("%s differs from its Baseline or Cache", name), code: 1}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagFetch, "fetch", false, "Refresh the skill's Source into the Cache first, without syncing")
	cmd.Flags().BoolVar(&flagStat, "stat", false, "Print changed files with their line counts only")
	return cmd
}

// fetchSkillSource refreshes the Source that declares name, exactly as
// update's refresh does, and stops there. A Skill that is not remote is left
// for DiffSkill to reject.
func fetchSkillSource(cmd *cobra.Command, cfg *config.Config, name, cacheDir string) error {
	category, source, found := config.FindSkillSource(cfg, name)
	if !found || category != "remote" {
		return nil
	}
	// Progress goes to stderr; stdout carries only the diff.
	result, err := refreshSources(cmd, io.Discard, cfg, []string{source}, false, false, false, cacheDir)
	if err != nil {
		return err
	}
	if len(result.Errors) > 0 {
		return fmt.Errorf("refresh Source %s: %s", source, result.Errors[0].Error)
	}
	return nil
}

func printSkillDiff(out io.Writer, d *engine.SkillDiff, stat bool, scopeFlag string) {
	head := shortCommit(d.CacheCommit)
	if d.BaselineCommit == "" {
		fmt.Fprintf(out, "%sUpstream%s  Scope copy %s %s %s\n", colorBold, colorReset, arrow, d.Source, head)
		switch d.NoBaseline {
		case engine.NoBaselineStateUnreadable:
			printScopeStateWarning(out, d.StateError, scopeFlag)
			fmt.Fprintf(out, "%sWithout the Scope state, upstream and local changes to %s cannot be told apart.%s\n", colorDim, d.Skill, colorReset)
		case engine.NoBaselineOtherCache:
			fmt.Fprintf(out, "%sThe Baseline of %s was applied from another Source or branch, so upstream and local changes cannot be told apart.%s\n", colorDim, d.Skill, colorReset)
		default:
			fmt.Fprintf(out, "%sNo Baseline is recorded for %s, so upstream and local changes cannot be told apart.%s\n", colorDim, d.Skill, colorReset)
		}
		printFileDiffs(out, d.Upstream, stat)
		return
	}
	fmt.Fprintf(out, "%sUpstream%s  %s %s %s %s\n", colorBold, colorReset, d.Source, shortCommit(d.BaselineCommit), arrow, head)
	printFileDiffs(out, d.Upstream, stat)
	fmt.Fprintln(out)
	fmt.Fprintf(out, "%sLocal%s  Baseline %s %s\n", colorBold, colorReset, arrow, models.ToTildePath(d.ScopePath))
	printFileDiffs(out, d.Local, stat)
}

func shortCommit(sha string) string {
	return sha[:min(7, len(sha))]
}

func printFileDiffs(out io.Writer, files []engine.FileDiff, stat bool) {
	if len(files) == 0 {
		fmt.Fprintf(out, "%sNo changes.%s\n", colorDim, colorReset)
		return
	}
	if stat {
		width := 0
		for _, file := range files {
			width = max(width, len(file.Path))
		}
		for _, file := range files {
			counts := fmt.Sprintf("%s+%d%s %s-%d%s", colorGreen, file.Added, colorReset, colorRed, file.Removed, colorReset)
			if file.Binary {
				counts = "binary"
			}
			fmt.Fprintf(out, " %-*s | %s\n", width, file.Path, counts)
		}
		return
	}
	for _, file := range files {
		inHunk := false
		for line := range strings.Lines(file.Patch) {
			text := strings.TrimSuffix(line, "\n")
			color := colorBold
			switch {
			case strings.HasPrefix(text, "@@"):
				inHunk = true
				color = colorCyan
			case !inHunk:
			case strings.HasPrefix(text, "+"):
				color = colorGreen
			case strings.HasPrefix(text, "-"):
				color = colorRed
			default:
				color = ""
			}
			if color == "" || colorReset == "" {
				fmt.Fprintln(out, text)
				continue
			}
			fmt.Fprintf(out, "%s%s%s\n", color, text, colorReset)
		}
	}
}
