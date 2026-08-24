package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

const (
	SyncRepoStart     = "repo_start"
	SyncRefreshStart  = "refresh_start"
	SyncRefreshDone   = "refresh_done"
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
	SyncSkipped       = "skipped"
	SyncInSync        = "in_sync"
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
	Changed    bool
	Failures   int
	Unknown    []SkillFreshness
}

type SyncOptions struct {
	Force        bool
	DryRun       bool
	AllowUnknown bool
}

func (r *SyncReport) add(ev SyncEvent) {
	r.Events = append(r.Events, ev)
}

func reconcileLocalSymlink(availability *Availability, name string, dryRun bool, emit func(SyncEvent)) error {
	if emit == nil {
		emit = func(SyncEvent) {}
	}
	info := availability.cfg.Local[name]
	absSource := models.ResolveLocalSourcePath(info.Source, availability.skillsDir)
	if _, err := os.Stat(absSource); err != nil {
		emit(SyncEvent{Kind: SyncSourceMissing, Skill: name, Path: absSource})
		return nil
	}
	if dryRun {
		dest := filepath.Join(availability.skillsDir, name)
		if target, err := os.Readlink(dest); err == nil && target == models.LocalSymlinkTarget(absSource, availability.skillsDir) {
			return availability.applyDeclared(name, "local", true, emit)
		}
		emit(SyncEvent{Kind: SyncWouldSymlink, Skill: name, Path: dest, Target: absSource})
		return availability.applyDeclared(name, "local", true, emit)
	}
	if err := MaterializeLocalSymlink(name, models.LocalSymlinkTarget(absSource, availability.skillsDir), availability.skillsDir); err != nil {
		emit(SyncEvent{Kind: SyncSymlinkFailed, Skill: name, Err: err.Error()})
		return nil
	}
	emit(SyncEvent{Kind: SyncSymlinked, Skill: name, Target: absSource})
	return availability.applyDeclared(name, "local", false, emit)
}

func reconcileCommand(availability *Availability, name string, dryRun bool, emit func(SyncEvent)) error {
	if emit == nil {
		emit = func(SyncEvent) {}
	}
	info := availability.cfg.Local[name]
	if info.Check != "" {
		if _, _, err := RunCmd(info.Check, ""); err != nil {
			emit(SyncEvent{Kind: SyncCheckFailed, Skill: name, Path: info.Check})
			return nil
		}
	}
	if dryRun {
		emit(SyncEvent{Kind: SyncWouldCommand, Skill: name, Target: info.Command})
		return availability.applyDeclared(name, "local", true, emit)
	}
	emit(SyncEvent{Kind: SyncCommandStart, Skill: name})
	if err := MaterializeCommand(info.Command); err != nil {
		emit(SyncEvent{Kind: SyncCommandFailed, Skill: name, Err: err.Error()})
	}
	return availability.applyDeclared(name, "local", false, emit)
}

// SyncDeclared materializes declared remote, local-symlink, and command Skills
// and applies Availability. Reconcile failures fail closed; fetch/copy/symlink
// failures are events and continue. A failed command installer is an event;
// Availability is still applied.
func SyncDeclared(cfg *config.Config, skillsDir, cacheDir string, force, dryRun bool, onProgress func(SyncEvent)) (*SyncReport, error) {
	return SyncDeclaredWithOptions(cfg, skillsDir, cacheDir, SyncOptions{Force: force, DryRun: dryRun}, onProgress)
}

func SyncDeclaredWithOptions(cfg *config.Config, skillsDir, cacheDir string, options SyncOptions, onProgress func(SyncEvent)) (*SyncReport, error) {
	report := &SyncReport{}
	availability := NewAvailability(cfg, skillsDir)
	emit := func(ev SyncEvent) {
		report.add(ev)
		switch ev.Kind {
		case SyncWouldDrift, SyncWouldSymlink, SyncWouldCommand:
			report.Changed = true
		case SyncSourceMissing, SyncSymlinkFailed, SyncCheckFailed, SyncCommandFailed:
			report.Failures++
		}
		if onProgress != nil {
			onProgress(ev)
		}
	}
	configured := make(map[string]struct{})

	plan, err := PlanScopeFreshness(cfg, skillsDir, cacheDir)
	if err != nil {
		return report, err
	}
	if plan.StateError != "" {
		emit(SyncEvent{Kind: SyncSkipped, Err: plan.StateError})
		report.Failures++
	}
	for _, repository := range plan.Repositories {
		emit(SyncEvent{Kind: SyncRepoStart, Source: repository.Source, Skills: skillNames(repository.Skills)})
		for _, skill := range repository.Skills {
			configured[skill.Name] = struct{}{}
			if skill.Status == SkillUnknownBaseline {
				report.Unknown = append(report.Unknown, skill)
			}
			if err := skill.validateCache(); err != nil {
				emit(SyncEvent{Kind: SyncFetchFailed, Source: skill.Source, Skill: skill.Name, Err: err.Error()})
				report.Failures++
				continue
			}
			write := skill.Status == SkillMissing || skill.Status == SkillCacheUpdateAvailable || options.Force || (skill.Status == SkillUnknownBaseline && options.AllowUnknown)
			blocked := (skill.Status == SkillLocalDrift && !options.Force) || (skill.Status == SkillUnknownBaseline && !options.Force && !options.AllowUnknown)
			if blocked {
				emit(SyncEvent{Kind: SyncSkipped, Source: skill.Source, Skill: skill.Name, Err: string(skill.Status)})
				report.Failures++
				continue
			}
			if options.DryRun {
				if write {
					emit(SyncEvent{Kind: SyncWouldSync, Source: skill.Source, Skill: skill.Name, Skills: []string{skill.Name}})
					report.Changed = true
				} else {
					emit(SyncEvent{Kind: SyncInSync, Source: skill.Source, Skill: skill.Name})
				}
				if err := availability.applyDeclared(skill.Name, skill.Source, true, emit); err != nil {
					report.Failures++
				}
				continue
			}
			if err := os.MkdirAll(skillsDir, 0o755); err != nil {
				emit(SyncEvent{Kind: SyncCopyFailed, Source: skill.Source, Skill: skill.Name, Err: err.Error()})
				report.Failures++
				continue
			}
			if write {
				if err := MaterializeRemoteSkill(skill.Name, skill.Subpath, repository.CachePath, skillsDir); err != nil {
					emit(SyncEvent{Kind: SyncCopyFailed, Source: skill.Source, Skill: skill.Name, Err: err.Error()})
					report.Failures++
					continue
				}
				emit(SyncEvent{Kind: SyncMaterialized, Source: skill.Source, Skill: skill.Name, Path: skill.Subpath})
				report.Changed = true
			} else {
				emit(SyncEvent{Kind: SyncInSync, Source: skill.Source, Skill: skill.Name})
			}
			if err := availability.applyDeclared(skill.Name, skill.Source, false, emit); err != nil {
				emit(SyncEvent{Kind: SyncCopyFailed, Source: skill.Source, Skill: skill.Name, Err: err.Error()})
				report.Failures++
				continue
			}
			if plan.StateError == "" {
				state := plan.State
				digests, digestErr := DigestSkillContent(skill.ScopePath)
				if digestErr != nil {
					report.Failures++
					continue
				}
				skill.CacheDigests = digests
				state.Skills[skill.Name] = skill.appliedState(repository.CachePath, repository.CacheSHA)
				if saveErr := plan.StateStore.Save(state); saveErr != nil {
					report.Failures++
					continue
				}
				plan.State = state
			}
		}
	}

	localNames := make([]string, 0, len(cfg.Local))
	for name := range cfg.Local {
		localNames = append(localNames, name)
	}
	sort.Strings(localNames)
	if !options.DryRun && len(localNames) > 0 {
		if err := os.MkdirAll(skillsDir, 0o755); err != nil {
			return report, fmt.Errorf("failed to create skills dir: %w", err)
		}
	}

	for _, name := range localNames {
		info := cfg.Local[name]
		if info.Type != "symlink" {
			continue
		}
		configured[name] = struct{}{}
		if err := reconcileLocalSymlink(availability, name, options.DryRun, emit); err != nil {
			report.Failures++
		}
	}

	for _, name := range localNames {
		info := cfg.Local[name]
		if info.Type != "command" {
			continue
		}
		configured[name] = struct{}{}
		if err := reconcileCommand(availability, name, options.DryRun, emit); err != nil {
			report.Failures++
		}
	}

	names := make([]string, 0, len(configured))
	for name := range configured {
		names = append(names, name)
	}
	sort.Strings(names)
	report.Configured = names
	if report.Failures > 0 || (options.DryRun && report.Changed) {
		return report, fmt.Errorf("Sync did not converge: %d failure(s)", report.Failures)
	}
	return report, nil
}

func skillNames(skills []SkillFreshness) []string {
	names := make([]string, 0, len(skills))
	for _, skill := range skills {
		names = append(names, skill.Name)
	}
	return names
}
