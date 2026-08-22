package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/akunzai/skills-manager/internal/config"
)

// LocalSyncSkill is a local symlink Source already resolved by the CLI for the
// active Scope.
type LocalSyncSkill struct {
	Name       string
	AbsSource  string
	LinkTarget string
}

// CommandSyncSkill is a command Source: an installer to run, with an optional
// check that skips the Skill when it fails.
type CommandSyncSkill struct {
	Name    string
	Command string
	Check   string
}

const (
	SyncRepoStart     = "repo_start"
	SyncFetchFailed   = "fetch_failed"
	SyncPathMissing   = "path_missing"
	SyncCopyFailed    = "copy_failed"
	SyncMaterialized  = "materialized"
	SyncWouldSync     = "would_sync"
	SyncWouldDrift    = "would_drift"
	SyncSourceMissing = "source_missing"
	SyncWouldSymlink  = "would_symlink"
	SyncSymlinkFailed = "symlink_failed"
	SyncSymlinked     = "symlinked"
	SyncCheckFailed   = "check_failed"
	SyncWouldCommand  = "would_command"
	SyncCommandStart  = "command_start"
	SyncCommandFailed = "command_failed"
)

// SyncEvent is one step of SyncDeclared for the CLI to print.
type SyncEvent struct {
	Kind       string
	Source     string
	Skill      string
	Skills     []string
	Path       string
	Target     string
	Err        string
	Missing    []string
	Unexpected []string
}

// SyncReport is the observable outcome of Syncing declared Config.
type SyncReport struct {
	Configured []string
	Events     []SyncEvent
}

func (r *SyncReport) add(ev SyncEvent) {
	r.Events = append(r.Events, ev)
}

func (r *SyncReport) driftEvent(name, source string, cfg *config.Config, skillsDir string) {
	missing, unexpected := AgentLinkDrift(name, source, cfg, skillsDir)
	if len(missing) == 0 && len(unexpected) == 0 {
		return
	}
	r.add(SyncEvent{Kind: SyncWouldDrift, Source: source, Skill: name, Missing: missing, Unexpected: unexpected})
}

// SyncDeclared materializes declared remote, local-symlink, and command Skills
// and applies Availability. Reconcile failures fail closed; fetch/copy/symlink
// failures are events and continue. A failed command installer is an event;
// Availability is still applied.
func SyncDeclared(cfg *config.Config, skillsDir, cacheDir string, locals []LocalSyncSkill, commands []CommandSyncSkill, force, dryRun bool) (*SyncReport, error) {
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create skills dir: %w", err)
	}
	report := &SyncReport{}
	configured := make(map[string]struct{})

	remoteSources := make([]string, 0, len(cfg.Remote))
	for source := range cfg.Remote {
		remoteSources = append(remoteSources, source)
	}
	sort.Strings(remoteSources)

	for _, source := range remoteSources {
		repoInfo := cfg.Remote[source]
		for sk := range repoInfo.Skills {
			configured[sk] = struct{}{}
		}

		missingSkills := make(map[string]string)
		for name, subpath := range repoInfo.Skills {
			if force {
				missingSkills[name] = subpath
				continue
			}
			if _, err := os.Stat(filepath.Join(skillsDir, name)); err != nil {
				missingSkills[name] = subpath
			}
		}

		if len(missingSkills) == 0 && !force {
			if dryRun {
				for _, name := range sortedSkillKeys(repoInfo.Skills) {
					report.driftEvent(name, source, cfg, skillsDir)
				}
				continue
			}
			for _, name := range sortedSkillKeys(repoInfo.Skills) {
				if err := ReconcileAgentSymlinks(name, source, cfg, skillsDir); err != nil {
					return report, fmt.Errorf("failed to reconcile availability for %s: %w", name, err)
				}
			}
			continue
		}

		report.add(SyncEvent{Kind: SyncRepoStart, Source: source, Skills: sortedSkillKeys(repoInfo.Skills)})
		if dryRun {
			report.add(SyncEvent{Kind: SyncWouldSync, Source: source, Skills: sortedSkillKeys(missingSkills)})
			for _, name := range sortedSkillKeys(repoInfo.Skills) {
				report.driftEvent(name, source, cfg, skillsDir)
			}
			continue
		}

		repoDir, err := EnsureGitRepo(source, repoInfo.URL, repoInfo.Branch, force, cacheDir)
		if err != nil {
			report.add(SyncEvent{Kind: SyncFetchFailed, Source: source, Err: err.Error()})
			continue
		}

		for _, name := range sortedSkillKeys(repoInfo.Skills) {
			subpath := repoInfo.Skills[name]
			_, needsWrite := missingSkills[name]
			if needsWrite {
				if err := MaterializeRemoteSkill(name, subpath, repoDir, skillsDir); err != nil {
					if errors.Is(err, errRepoPathMissing) {
						report.add(SyncEvent{Kind: SyncPathMissing, Source: source, Skill: name, Path: subpath})
					} else {
						report.add(SyncEvent{Kind: SyncCopyFailed, Source: source, Skill: name, Err: err.Error()})
					}
					continue
				}
				report.add(SyncEvent{Kind: SyncMaterialized, Skill: name})
			}
			if err := ReconcileAgentSymlinks(name, source, cfg, skillsDir); err != nil {
				return report, fmt.Errorf("failed to reconcile availability for %s: %w", name, err)
			}
		}
	}

	sort.Slice(locals, func(i, j int) bool { return locals[i].Name < locals[j].Name })
	for _, local := range locals {
		configured[local.Name] = struct{}{}
		if _, err := os.Stat(local.AbsSource); err != nil {
			report.add(SyncEvent{Kind: SyncSourceMissing, Skill: local.Name, Path: local.AbsSource})
			continue
		}
		dest := filepath.Join(skillsDir, local.Name)
		if dryRun {
			report.add(SyncEvent{Kind: SyncWouldSymlink, Skill: local.Name, Path: dest, Target: local.AbsSource})
			report.driftEvent(local.Name, "local", cfg, skillsDir)
			continue
		}
		if err := MaterializeLocalSymlink(local.Name, local.LinkTarget, skillsDir); err != nil {
			report.add(SyncEvent{Kind: SyncSymlinkFailed, Skill: local.Name, Err: err.Error()})
			continue
		}
		report.add(SyncEvent{Kind: SyncSymlinked, Skill: local.Name, Target: local.AbsSource})
		if err := ReconcileAgentSymlinks(local.Name, "local", cfg, skillsDir); err != nil {
			return report, fmt.Errorf("failed to reconcile availability for %s: %w", local.Name, err)
		}
	}

	sort.Slice(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
	for _, cmdSkill := range commands {
		configured[cmdSkill.Name] = struct{}{}
		if cmdSkill.Check != "" {
			if _, _, err := RunCmd(cmdSkill.Check, ""); err != nil {
				report.add(SyncEvent{Kind: SyncCheckFailed, Skill: cmdSkill.Name, Path: cmdSkill.Check})
				continue
			}
		}
		if dryRun {
			report.add(SyncEvent{Kind: SyncWouldCommand, Skill: cmdSkill.Name, Target: cmdSkill.Command})
			report.driftEvent(cmdSkill.Name, "local", cfg, skillsDir)
			continue
		}
		report.add(SyncEvent{Kind: SyncCommandStart, Skill: cmdSkill.Name})
		if err := MaterializeCommand(cmdSkill.Command); err != nil {
			report.add(SyncEvent{Kind: SyncCommandFailed, Skill: cmdSkill.Name, Err: err.Error()})
		}
		if err := ReconcileAgentSymlinks(cmdSkill.Name, "local", cfg, skillsDir); err != nil {
			return report, fmt.Errorf("failed to reconcile availability for %s: %w", cmdSkill.Name, err)
		}
	}

	names := make([]string, 0, len(configured))
	for name := range configured {
		names = append(names, name)
	}
	sort.Strings(names)
	report.Configured = names
	return report, nil
}
