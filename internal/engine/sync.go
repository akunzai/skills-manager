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

func reconcileLocalSymlink(cfg *config.Config, name, skillsDir string, dryRun bool, emit func(SyncEvent)) error {
	if emit == nil {
		emit = func(SyncEvent) {}
	}
	info := cfg.Local[name]
	absSource := models.ResolveLocalSourcePath(info.Source, skillsDir)
	if _, err := os.Stat(absSource); err != nil {
		emit(SyncEvent{Kind: SyncSourceMissing, Skill: name, Path: absSource})
		return nil
	}
	if dryRun {
		dest := filepath.Join(skillsDir, name)
		emit(SyncEvent{Kind: SyncWouldSymlink, Skill: name, Path: dest, Target: absSource})
		return applyDeclaredAvailability(name, "local", cfg, skillsDir, true, emit)
	}
	if err := MaterializeLocalSymlink(name, models.LocalSymlinkTarget(absSource, skillsDir), skillsDir); err != nil {
		emit(SyncEvent{Kind: SyncSymlinkFailed, Skill: name, Err: err.Error()})
		return nil
	}
	emit(SyncEvent{Kind: SyncSymlinked, Skill: name, Target: absSource})
	return applyDeclaredAvailability(name, "local", cfg, skillsDir, false, emit)
}

func reconcileCommand(cfg *config.Config, name, skillsDir string, dryRun bool, emit func(SyncEvent)) error {
	if emit == nil {
		emit = func(SyncEvent) {}
	}
	info := cfg.Local[name]
	if info.Check != "" {
		if _, _, err := RunCmd(info.Check, ""); err != nil {
			emit(SyncEvent{Kind: SyncCheckFailed, Skill: name, Path: info.Check})
			return nil
		}
	}
	if dryRun {
		emit(SyncEvent{Kind: SyncWouldCommand, Skill: name, Target: info.Command})
		return applyDeclaredAvailability(name, "local", cfg, skillsDir, true, emit)
	}
	emit(SyncEvent{Kind: SyncCommandStart, Skill: name})
	if err := MaterializeCommand(info.Command); err != nil {
		emit(SyncEvent{Kind: SyncCommandFailed, Skill: name, Err: err.Error()})
	}
	return applyDeclaredAvailability(name, "local", cfg, skillsDir, false, emit)
}

// SyncDeclared materializes declared remote, local-symlink, and command Skills
// and applies Availability. Reconcile failures fail closed; fetch/copy/symlink
// failures are events and continue. A failed command installer is an event;
// Availability is still applied.
func SyncDeclared(cfg *config.Config, skillsDir, cacheDir string, force, dryRun bool, onProgress func(SyncEvent)) (*SyncReport, error) {
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create skills dir: %w", err)
	}
	report := &SyncReport{}
	emit := func(ev SyncEvent) {
		report.add(ev)
		if onProgress != nil {
			onProgress(ev)
		}
	}
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

		if err := newRemoteSource(cfg, source, repoInfo, skillsDir, cacheDir).sync(force, dryRun, emit); err != nil {
			return report, err
		}
	}

	localNames := make([]string, 0, len(cfg.Local))
	for name := range cfg.Local {
		localNames = append(localNames, name)
	}
	sort.Strings(localNames)

	for _, name := range localNames {
		info := cfg.Local[name]
		if info.Type != "symlink" {
			continue
		}
		configured[name] = struct{}{}
		if err := reconcileLocalSymlink(cfg, name, skillsDir, dryRun, emit); err != nil {
			return report, err
		}
	}

	for _, name := range localNames {
		info := cfg.Local[name]
		if info.Type != "command" {
			continue
		}
		configured[name] = struct{}{}
		if err := reconcileCommand(cfg, name, skillsDir, dryRun, emit); err != nil {
			return report, err
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
