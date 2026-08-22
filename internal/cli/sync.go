package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
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
			out := cmd.OutOrStdout()
			configPath, skillsDir, cacheDir := GetEffectivePaths()

			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return err
			}
			if flagPrune || flagPruneOnly {
				return runPrune(cmd, pruneOptions{yes: flagYes, dryRun: flagDryRun})
			}
			previewAvailability := func(name, source string) {
				missing, unexpected := engine.AgentLinkDrift(name, source, cfg, skillsDir)
				if len(missing) > 0 {
					fmt.Fprintf(out, "  [Dry-run] Would link %s to %s.\n", name, strings.Join(missing, ", "))
				}
				if len(unexpected) > 0 {
					fmt.Fprintf(out, "  [Dry-run] Would unlink %s from %s.\n", name, strings.Join(unexpected, ", "))
				}
			}

			fmt.Fprintf(out, "\n%s%sSyncing skills from %s...%s\n\n", colorBold, colorCyan, models.ToTildePath(configPath), colorReset)

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
					if flagDryRun {
						for name := range repoInfo.Skills {
							previewAvailability(name, source)
						}
						continue
					}
					for name := range repoInfo.Skills {
						if err := engine.ReconcileAgentSymlinks(name, source, cfg, skillsDir); err != nil {
							return fmt.Errorf("failed to reconcile availability for %s: %w", name, err)
						}
					}
					continue
				}

				fmt.Fprintf(out, "Syncing repo: %s%s%s (%d skills)...\n", colorBold, source, colorReset, len(repoInfo.Skills))
				if flagDryRun {
					missingNames := make([]string, 0, len(missingSkills))
					for k := range missingSkills {
						missingNames = append(missingNames, k)
					}
					fmt.Fprintf(out, "  [Dry-run] Would sync %s from %s\n", strings.Join(missingNames, ", "), source)
					for name := range repoInfo.Skills {
						previewAvailability(name, source)
					}
					continue
				}

				repoDir, err := engine.EnsureGitRepo(source, repoInfo.URL, repoInfo.Branch, flagForce, cacheDir)
				if err != nil {
					fmt.Fprintf(out, "  %sFailed to fetch %s: %s%s\n", colorRed, source, err, colorReset)
					continue
				}

				for name, subpath := range repoInfo.Skills {
					srcPath := filepath.Join(repoDir, filepath.FromSlash(subpath))
					targetPath := filepath.Join(skillsDir, name)

					if _, err := os.Stat(srcPath); err != nil {
						fmt.Fprintf(out, "  %sSkill path missing in repo: %s for %s%s\n", colorRed, subpath, name, colorReset)
						continue
					}

					if flagForce || !func() bool { _, err := os.Stat(targetPath); return err == nil }() {
						if err := engine.CopySkillFolder(srcPath, targetPath); err != nil {
							fmt.Fprintf(out, "  %sFailed to copy %s: %s%s\n", colorRed, name, err, colorReset)
							continue
						}
						fmt.Fprintf(out, "  %sRestored %s%s%s.%s\n", colorGreen, colorBold, name, colorReset, colorReset)
					}

					if err := engine.ReconcileAgentSymlinks(name, source, cfg, skillsDir); err != nil {
						return fmt.Errorf("failed to reconcile availability for %s: %w", name, err)
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
						fmt.Fprintf(out, "  %sWarning: Local symlink source missing: %s (skill: %s)%s\n", colorYellow, models.ToTildePath(src), name, colorReset)
						continue
					}

					if flagDryRun {
						fmt.Fprintf(out, "  [Dry-run] Would symlink %s -> %s\n", models.ToTildePath(targetLink), models.ToTildePath(src))
						previewAvailability(name, "local")
					} else {
						if err := engine.CreateSymlink(LocalSymlinkTarget(src, skillsDir), targetLink, true); err != nil {
							fmt.Fprintf(out, "  %sFailed to symlink %s: %s%s\n", colorRed, name, err, colorReset)
							continue
						}
						fmt.Fprintf(out, "  %sLinked local skill %s%s%s -> %s.%s\n", colorGreen, colorBold, name, colorReset, models.ToTildePath(src), colorReset)

						if err := engine.ReconcileAgentSymlinks(name, "local", cfg, skillsDir); err != nil {
							return fmt.Errorf("failed to reconcile availability for %s: %w", name, err)
						}
					}
				} else if localInfo.Type == "command" {
					cmdStr := localInfo.Command
					checkStr := localInfo.Check

					if checkStr != "" {
						_, _, err := engine.RunCmd(checkStr, "")
						if err != nil {
							fmt.Fprintf(out, "  %sCommand check '%s' failed, skipping %s%s\n", colorDim, checkStr, name, colorReset)
							continue
						}
					}

					if flagDryRun {
						fmt.Fprintf(out, "  [Dry-run] Would execute: %s\n", cmdStr)
						previewAvailability(name, "local")
					} else {
						fmt.Fprintf(out, "  Running installer for %s%s%s...\n", colorBold, name, colorReset)
						_, _, _ = engine.RunCmd(cmdStr, "")

						if err := engine.ReconcileAgentSymlinks(name, "local", cfg, skillsDir); err != nil {
							return fmt.Errorf("failed to reconcile availability for %s: %w", name, err)
						}
					}
				}
			}

			// 3. Post-hooks
			if len(cfg.PostHooks) > 0 {
				fmt.Fprintf(out, "\n%sRunning post-sync hooks...%s\n", colorCyan, colorReset)
				hookResults := engine.ExecutePostHooks(cfg.PostHooks, flagDryRun)
				for _, h := range hookResults {
					badge := fmt.Sprintf("%sOK%s", colorGreen, colorReset)
					if !h.Success {
						badge = fmt.Sprintf("%sError%s", colorRed, colorReset)
					}
					fmt.Fprintf(out, "  %s [%s] %s\n", badge, h.Name, h.Message)
				}
			}

			fmt.Fprintf(out, "\n%s%sSkills sync complete. %d skills configured.%s\n\n", colorBold, colorGreen, len(configuredSkills), colorReset)
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
