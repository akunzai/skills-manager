package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	var (
		flagForce     bool
		flagPrune     bool
		flagPruneOnly bool
		flagDryRun    bool
		flagYes       bool
	)

	cmd := &cobra.Command{
		Use:     "sync",
		Aliases: []string{"restore"},
		Short:   "Sync and restore all skills declared in skills.json",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Past flag parsing, every failure below is a runtime problem rather
			// than misuse, so reporting it with a usage dump would mislead.
			cmd.SilenceUsage = true
			configPath, skillsDir, cacheDir := GetEffectivePaths()

			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}
			if flagPrune || flagPruneOnly {
				return runPrune(cmd, pruneOptions{yes: flagYes, dryRun: flagDryRun})
			}

			fmt.Printf("\n%s%s🚀 Syncing skills from %s...%s\n\n", colorBold, colorCyan, configPath, colorReset)

			if err := os.MkdirAll(skillsDir, 0755); err != nil {
				return err
			}

			configuredSkills := make(map[string]struct{})

			// 1. Sync Remote Skills
			for source, repoInfo := range cfg.Remote {
				for sk := range repoInfo.Skills {
					configuredSkills[sk] = struct{}{}
				}

				missingSkills := make(map[string]string)
				for name, subpath := range repoInfo.Skills {
					targetPath := filepath.Join(skillsDir, name)
					if flagForce {
						missingSkills[name] = subpath
					} else if _, err := os.Stat(targetPath); err != nil {
						missingSkills[name] = subpath
					}
				}

				if len(missingSkills) == 0 && !flagForce {
					for name := range repoInfo.Skills {
						for _, agent := range engine.GetTargetAgentsForSkill(name, source, cfg, skillsDir) {
							_, _ = engine.EnsureAgentSymlink(name, agent, skillsDir)
						}
					}
					continue
				}

				fmt.Printf("📦 Syncing repo: %s%s%s (%d skills)...\n", colorBold, source, colorReset, len(repoInfo.Skills))
				if flagDryRun {
					missingNames := make([]string, 0, len(missingSkills))
					for k := range missingSkills {
						missingNames = append(missingNames, k)
					}
					fmt.Printf("  [Dry-run] Would sync %s from %s\n", strings.Join(missingNames, ", "), source)
					continue
				}

				repoDir, err := engine.EnsureGitRepo(source, repoInfo.URL, repoInfo.Branch, flagForce, cacheDir)
				if err != nil {
					fmt.Printf("  %s✖ Failed to fetch %s: %s%s\n", colorRed, source, err, colorReset)
					continue
				}

				for name, subpath := range repoInfo.Skills {
					srcPath := filepath.Join(repoDir, filepath.FromSlash(subpath))
					targetPath := filepath.Join(skillsDir, name)

					if _, err := os.Stat(srcPath); err != nil {
						fmt.Printf("  %s✖ Skill path missing in repo: %s for %s%s\n", colorRed, subpath, name, colorReset)
						continue
					}

					if flagForce || !func() bool { _, err := os.Stat(targetPath); return err == nil }() {
						if err := engine.CopySkillFolder(srcPath, targetPath); err != nil {
							fmt.Printf("  %s✖ Failed to copy %s: %s%s\n", colorRed, name, err, colorReset)
							continue
						}
						fmt.Printf("  %s✔%s Restored %s%s%s\n", colorGreen, colorReset, colorBold, name, colorReset)
					}

					for _, agent := range engine.GetTargetAgentsForSkill(name, source, cfg, skillsDir) {
						_, _ = engine.EnsureAgentSymlink(name, agent, skillsDir)
					}
				}
			}

			// 2. Sync Local Skills
			for name, localInfo := range cfg.Local {
				configuredSkills[name] = struct{}{}

				if localInfo.Type == "symlink" {
					src := ResolveLocalSourcePath(localInfo.Source, skillsDir)
					targetLink := filepath.Join(skillsDir, name)
					if _, err := os.Stat(src); err != nil {
						fmt.Printf("  %s⚠️  Local symlink source missing: %s (skill: %s)%s\n", colorYellow, src, name, colorReset)
						continue
					}

					if flagDryRun {
						fmt.Printf("  [Dry-run] Would symlink %s -> %s\n", targetLink, src)
					} else {
						if err := engine.CreateSymlink(LocalSymlinkTarget(src, skillsDir), targetLink, true); err != nil {
							fmt.Printf("  %s✖ Failed to symlink %s: %s%s\n", colorRed, name, err, colorReset)
							continue
						}
						fmt.Printf("  %s✔%s Linked local skill %s%s%s -> %s\n", colorGreen, colorReset, colorBold, name, colorReset, src)

						for _, agent := range engine.GetTargetAgentsForSkill(name, "local", cfg, skillsDir) {
							_, _ = engine.EnsureAgentSymlink(name, agent, skillsDir)
						}
					}
				} else if localInfo.Type == "command" {
					cmdStr := localInfo.Command
					checkStr := localInfo.Check

					if checkStr != "" {
						_, _, err := engine.RunCmd(checkStr, "")
						if err != nil {
							fmt.Printf("  %sCommand check '%s' failed, skipping %s%s\n", colorDim, checkStr, name, colorReset)
							continue
						}
					}

					if flagDryRun {
						fmt.Printf("  [Dry-run] Would execute: %s\n", cmdStr)
					} else {
						fmt.Printf("  ⚙️  Running installer for %s%s%s...\n", colorBold, name, colorReset)
						_, _, _ = engine.RunCmd(cmdStr, "")

						for _, agent := range engine.GetTargetAgentsForSkill(name, "local", cfg, skillsDir) {
							_, _ = engine.EnsureAgentSymlink(name, agent, skillsDir)
						}
					}
				}
			}

			// 3. Post-hooks
			if len(cfg.PostHooks) > 0 {
				fmt.Printf("\n%s⚡ Running post-sync hooks...%s\n", colorCyan, colorReset)
				hookResults := engine.ExecutePostHooks(cfg.PostHooks, flagDryRun)
				for _, h := range hookResults {
					badge := fmt.Sprintf("%s✔%s", colorGreen, colorReset)
					if !h.Success {
						badge = fmt.Sprintf("%s✖%s", colorRed, colorReset)
					}
					fmt.Printf("  %s [%s] %s\n", badge, h.Name, h.Message)
				}
			}

			fmt.Printf("\n%s%s✨ Skills sync complete! (%d skills configured)%s\n\n", colorBold, colorGreen, len(configuredSkills), colorReset)
			return nil
		},
	}

	cmd.Flags().BoolVar(&flagForce, "force", false, "Force re-clone and re-link all skills")
	cmd.Flags().BoolVar(&flagPrune, "prune", false, "Remove untracked skills and broken symlinks")
	cmd.Flags().BoolVar(&flagPruneOnly, "prune-only", false, "Remove untracked skills without restoring")
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip confirmation prompt when using deprecated prune flags")
	_ = cmd.Flags().MarkDeprecated("prune", "use `skills prune` instead")
	_ = cmd.Flags().MarkDeprecated("prune-only", "use `skills prune` instead")
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Preview actions without making changes")

	return cmd
}
