package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/models"
	"github.com/akunzai/skills-manager/internal/tui"
	"github.com/spf13/cobra"
)

// sourceLabels carries the display text that differs between install
// sources. docs/design.md requires an action's verb to stay consistent from
// prompt through result, so promptVerb/pastVerb travel together with the
// source rather than being chosen ad hoc at each print site.
type sourceLabels struct {
	displayName  string // shown in interactive prompt titles
	resourceNoun string // "Local directory" / "Repository", for the "contains multiple skills" message
	promptVerb   string // "link" / "install"
	failVerb     string // "symlink" / "install", for the install-failure error
	pastVerb     string // "Linked" / "Installed"
	unitNoun     string // "local skill(s)" / "skill(s)", for the final summary line
}

// installSource is the seam between the add command's shared selection and
// bookkeeping pipeline and how a skill actually gets materialized: a symlink
// for a local directory, a copy for a cloned repository.
type installSource interface {
	// rootDir is the source's own filesystem root, used to resolve --path
	// against a subdirectory when it doesn't match anything discovery found.
	rootDir() string
	labels() sourceLabels
	// configSourceKey identifies the source in skills.json: "local" for
	// symlinked skills, or the git source key for remote ones.
	configSourceKey() string
	// confirmReplacementArgs supplies confirmSkillReplacements' source-specific
	// display arguments.
	confirmReplacementArgs() (newSourceKey string, isLocal bool, absSourcePath string)
	install(name, subpath, skillsDir string) error
	recordConfig(cfg *config.Config, name, subpath, skillsDir string)
	progressLine(name, subpath string) string
	// allowsSoleDiscoveredRename reports whether a single --skill flag against
	// a single discovered skill renames it outright rather than requiring an
	// exact/case-insensitive name match. Local-only: it predates this shared
	// pipeline and is the convenience of pointing --symlink at one skill under
	// a different name; remote installs never had this shortcut.
	allowsSoleDiscoveredRename() bool
}

type localInstallSource struct {
	absSourcePath string
	description   string
}

func (s *localInstallSource) rootDir() string { return s.absSourcePath }

func (s *localInstallSource) resolvedPath(subpath string) string {
	if subpath != "" && subpath != "." {
		return filepath.Join(s.absSourcePath, filepath.FromSlash(subpath))
	}
	return s.absSourcePath
}

func (s *localInstallSource) labels() sourceLabels {
	return sourceLabels{
		displayName:  models.ToTildePath(s.absSourcePath),
		resourceNoun: "Local directory",
		promptVerb:   "link",
		failVerb:     "symlink",
		pastVerb:     "Linked",
		unitNoun:     "local skill(s)",
	}
}

func (s *localInstallSource) configSourceKey() string { return "local" }

func (s *localInstallSource) allowsSoleDiscoveredRename() bool { return true }

func (s *localInstallSource) confirmReplacementArgs() (string, bool, string) {
	return "", true, s.absSourcePath
}

func (s *localInstallSource) install(name, subpath, skillsDir string) error {
	skillSource := s.resolvedPath(subpath)
	return engine.MaterializeLocalSymlink(name, LocalSymlinkTarget(skillSource, skillsDir), skillsDir)
}

func (s *localInstallSource) recordConfig(cfg *config.Config, name, subpath, skillsDir string) {
	config.AddLocalSymlinkEntry(cfg, name, StoreLocalSourcePath(s.resolvedPath(subpath), skillsDir), s.description)
}

func (s *localInstallSource) progressLine(name, subpath string) string {
	return fmt.Sprintf("Linking local skill: %s%s%s -> %s", colorBold, name, colorReset, models.ToTildePath(s.resolvedPath(subpath)))
}

type remoteInstallSource struct {
	repoDir   string
	sourceKey string
	repoType  string
	storedURL string
}

func (s *remoteInstallSource) rootDir() string { return s.repoDir }

func (s *remoteInstallSource) labels() sourceLabels {
	return sourceLabels{
		displayName:  s.sourceKey,
		resourceNoun: "Repository",
		promptVerb:   "install",
		failVerb:     "install",
		pastVerb:     "Installed",
		unitNoun:     "skill(s)",
	}
}

func (s *remoteInstallSource) configSourceKey() string { return s.sourceKey }

func (s *remoteInstallSource) allowsSoleDiscoveredRename() bool { return false }

func (s *remoteInstallSource) confirmReplacementArgs() (string, bool, string) {
	return s.sourceKey, false, ""
}

func (s *remoteInstallSource) install(name, subpath, skillsDir string) error {
	return engine.MaterializeRemoteSkill(name, subpath, s.repoDir, skillsDir)
}

func (s *remoteInstallSource) recordConfig(cfg *config.Config, name, subpath, _ string) {
	config.AddRemoteSkillEntry(cfg, s.sourceKey, name, subpath, s.repoType, s.storedURL)
}

func (s *remoteInstallSource) progressLine(name, subpath string) string {
	return fmt.Sprintf("Installing %s%s%s (from %s)...", colorBold, name, colorReset, subpath)
}

func sortedDiscoveredNames(discovered map[string]string) []string {
	names := make([]string, 0, len(discovered))
	for name := range discovered {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// resolveSkillsToInstall turns --all/--path/--skill or an interactive prompt
// into the set of skills to install. cancelled reports a user-cancelled
// selection, which the caller must treat as a silent success, not an error.
func resolveSkillsToInstall(
	cmd *cobra.Command,
	discovered map[string]string,
	src installSource,
	flagAll bool,
	pathOverride string,
	flagSkills []string,
	flagYes bool,
) (skillsToInstall map[string]string, cancelled bool, err error) {
	out := cmd.OutOrStdout()
	labels := src.labels()
	skillsToInstall = make(map[string]string)

	switch {
	case flagAll:
		skillsToInstall = discovered

	case pathOverride != "" && len(flagSkills) == 0:
		cleanSub := filepath.ToSlash(strings.Trim(pathOverride, "/"))
		for k, v := range discovered {
			if filepath.ToSlash(strings.Trim(v, "/")) == cleanSub {
				skillsToInstall[k] = v
			}
		}
		if len(skillsToInstall) == 0 {
			subDir := filepath.Join(src.rootDir(), filepath.FromSlash(pathOverride))
			if _, statErr := os.Stat(filepath.Join(subDir, "SKILL.md")); statErr == nil {
				skillsToInstall[filepath.Base(subDir)] = pathOverride
			} else {
				return nil, false, fmt.Errorf("specified path '%s' does not contain SKILL.md", pathOverride)
			}
		}

	case len(flagSkills) > 0:
		if len(discovered) == 1 && len(flagSkills) == 1 && src.allowsSoleDiscoveredRename() {
			for _, sub := range discovered {
				skillsToInstall[flagSkills[0]] = sub
			}
		} else {
			for _, sk := range flagSkills {
				if sub, ok := discovered[sk]; ok {
					skillsToInstall[sk] = sub
				} else if pathOverride != "" {
					skillsToInstall[sk] = pathOverride
				} else {
					matched := false
					for k, v := range discovered {
						if strings.EqualFold(k, sk) {
							skillsToInstall[k] = v
							matched = true
							break
						}
					}
					if !matched {
						fmt.Fprintf(out, "%sWarning: Skill '%s' not found in discovered list (%s)%s\n", colorYellow, sk, strings.Join(sortedDiscoveredNames(discovered), ", "), colorReset)
					}
				}
			}
		}

	default:
		if shouldPromptForDiscoveredSkills(len(discovered), tui.IsTerminal(), flagYes) {
			groups, shouldGroup := groupDiscoveredSkills(discovered)
			selectionDirs := selectionSkillsDirs(cmd)

			var chosen []string
			var promptErr error
			promptTitle := fmt.Sprintf("Select skills to %s from %s:", labels.promptVerb, labels.displayName)
			if shouldGroup {
				for _, options := range groups {
					markInstalledSkills(options, selectionDirs)
				}
				chosen, promptErr = tui.PromptGroupedMultiSelect(promptTitle, groups)
			} else {
				options := make([]tui.SelectOption, 0, len(discovered))
				for skName := range discovered {
					options = append(options, tui.SelectOption{Key: skName, Title: skName})
				}
				markInstalledSkills(options, selectionDirs)
				sort.Slice(options, func(i, j int) bool {
					return options[i].Key < options[j].Key
				})
				chosen, promptErr = tui.PromptMultiSelect(promptTitle, options)
			}
			if promptErr != nil {
				return nil, false, promptErr
			}
			if chosen == nil {
				fmt.Fprintf(out, "%sOperation cancelled.%s\n", colorYellow, colorReset)
				return nil, true, nil
			}
			if len(chosen) == 0 {
				fmt.Fprintf(out, "%sNo skills selected. Aborted.%s\n", colorYellow, colorReset)
				return nil, true, nil
			}
			for _, ch := range chosen {
				skillsToInstall[ch] = discovered[ch]
			}
		} else if len(discovered) == 1 {
			skillsToInstall = discovered
		} else {
			fmt.Fprintf(out, "%s%s contains multiple skills:%s %s\n", colorYellow, labels.resourceNoun, colorReset, strings.Join(sortedDiscoveredNames(discovered), ", "))
			fmt.Fprintf(out, "Please specify --skill <name> or --all\n")
			return nil, false, fmt.Errorf("multiple skills found without selection")
		}
	}

	return skillsToInstall, false, nil
}

// runAddPipeline resolves which skills to install, confirms replacements,
// materializes each skill through src, and reconciles agent availability.
// Both the local-symlink and remote-git add modes call this after acquiring
// their own source (a local directory or a cloned repository).
func runAddPipeline(
	cmd *cobra.Command,
	discovered map[string]string,
	src installSource,
	flagAll bool,
	pathOverride string,
	flagSkills []string,
	flagYes bool,
	flagAgents []string,
) error {
	out := cmd.OutOrStdout()

	skillsToInstall, cancelled, err := resolveSkillsToInstall(cmd, discovered, src, flagAll, pathOverride, flagSkills, flagYes)
	if err != nil {
		return err
	}
	if cancelled {
		return nil
	}
	if len(skillsToInstall) == 0 {
		return fmt.Errorf("no matching skills to install")
	}

	configPath, skillsDir, cfg, agents, err := prepareAddTarget(cmd, flagYes, flagAgents)
	if err != nil {
		return err
	}
	flagAgents = agents
	if err := promptAddAvailability(cfg, skillsToInstall, src.configSourceKey(), skillsDir, flagYes, flagAgents); err != nil {
		return err
	}

	newSourceKey, isLocal, absSourcePath := src.confirmReplacementArgs()
	if err := confirmSkillReplacements(cfg, skillsDir, skillsToInstall, newSourceKey, isLocal, absSourcePath, flagYes, out); err != nil {
		if err.Error() == "operation cancelled by user" {
			fmt.Fprintf(out, "%sOperation cancelled.%s\n", colorYellow, colorReset)
			return nil
		}
		return err
	}

	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		return err
	}

	labels := src.labels()
	var installedNames []string
	for name, subpath := range skillsToInstall {
		fmt.Fprintf(out, "  %s\n", src.progressLine(name, subpath))
		if err := src.install(name, subpath, skillsDir); err != nil {
			return fmt.Errorf("failed to %s skill %s: %w", labels.failVerb, name, err)
		}
		src.recordConfig(cfg, name, subpath, skillsDir)
		if len(flagAgents) > 0 {
			if err := config.IncludeSkillAgents(cfg, name, flagAgents...); err != nil {
				return err
			}
		}
		installedNames = append(installedNames, name)
	}

	if err := config.SaveConfig(cfg, configPath); err != nil {
		return err
	}
	for _, name := range installedNames {
		if err := engine.ReconcileAgentSymlinks(name, src.configSourceKey(), cfg, skillsDir); err != nil {
			return fmt.Errorf("saved config but failed to reconcile availability for %s: %w", name, err)
		}
	}

	sort.Strings(installedNames)
	fmt.Fprintf(out, "\n%s%s %d %s [%s] and updated %s.%s\n\n", colorGreen, labels.pastVerb, len(installedNames), labels.unitNoun, strings.Join(installedNames, ", "), filepath.Base(configPath), colorReset)
	return nil
}
