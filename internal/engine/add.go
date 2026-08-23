package engine

import (
	"fmt"
	"path/filepath"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// Adder declares selected Skills from one Source, saves Config, Materializes
// them, and applies Availability.
type Adder struct {
	cfg         *config.Config
	configPath  string
	skillsDir   string
	record      func(name, subpath string)
	materialize func(*Availability, string, string, func(SyncEvent)) error
}

func NewRemoteAdder(cfg *config.Config, configPath, skillsDir, repoDir, sourceKey, repoType, url string) *Adder {
	return &Adder{
		cfg: cfg, configPath: configPath, skillsDir: skillsDir,
		record: func(name, subpath string) {
			config.AddRemoteSkillEntry(cfg, sourceKey, name, subpath, repoType, url)
		},
		materialize: func(availability *Availability, name, subpath string, note func(SyncEvent)) error {
			one := map[string]string{name: subpath}
			return newRemoteSource(availability, sourceKey, config.RemoteRepo{Skills: one}, "").reconcile(repoDir, false, one, note)
		},
	}
}

func NewSymlinkAdder(cfg *config.Config, configPath, skillsDir, absSourcePath, description string) *Adder {
	resolvedPath := func(subpath string) string {
		if subpath != "" && subpath != "." {
			return filepath.Join(absSourcePath, filepath.FromSlash(subpath))
		}
		return absSourcePath
	}
	return &Adder{
		cfg: cfg, configPath: configPath, skillsDir: skillsDir,
		record: func(name, subpath string) {
			config.AddLocalSymlinkEntry(cfg, name, models.StoreLocalSourcePath(resolvedPath(subpath), skillsDir), description)
		},
		materialize: func(availability *Availability, name, _ string, note func(SyncEvent)) error {
			return reconcileLocalSymlink(availability, name, false, note)
		},
	}
}

func NewCommandAdder(cfg *config.Config, configPath, skillsDir, command, check, description string) *Adder {
	return &Adder{
		cfg: cfg, configPath: configPath, skillsDir: skillsDir,
		record: func(name, _ string) {
			config.AddLocalCommandEntry(cfg, name, command, check, description)
		},
		materialize: func(availability *Availability, name, _ string, note func(SyncEvent)) error {
			return reconcileCommand(availability, name, false, note)
		},
	}
}

// Run records all selected Skills before Materializing them. Copy, symlink,
// command, and check failures stop at that Skill; the saved declaration stays
// available for a later Sync. Availability still applies after command failure.
func (a *Adder) Run(skills map[string]string, agents []string, progress func(name, subpath string)) error {
	availability := NewAvailability(a.cfg, a.skillsDir)
	names := sortedSkillKeys(skills)
	for _, name := range names {
		a.record(name, skills[name])
		if len(agents) > 0 {
			if err := availability.Include(name, agents...); err != nil {
				return err
			}
		}
	}
	if err := config.SaveConfig(a.cfg, a.configPath); err != nil {
		return err
	}
	var stepErr error
	note := func(ev SyncEvent) {
		switch ev.Kind {
		case SyncCheckFailed:
			stepErr = fmt.Errorf("command check %q failed, skipping %s", ev.Path, ev.Skill)
		case SyncCommandFailed:
			stepErr = fmt.Errorf("%s", ev.Err)
		case SyncSymlinkFailed:
			stepErr = fmt.Errorf("%s", ev.Err)
		case SyncCopyFailed:
			stepErr = fmt.Errorf("%s", ev.Err)
		case SyncPathMissing:
			stepErr = fmt.Errorf("path missing in repository: %s", ev.Path)
		}
	}

	for _, name := range names {
		subpath := skills[name]
		if progress != nil {
			progress(name, subpath)
		}
		stepErr = nil
		err := a.materialize(availability, name, subpath, note)
		if err != nil {
			return fmt.Errorf("saved config but failed to apply availability for %s: %w", name, err)
		}
		if stepErr != nil {
			return fmt.Errorf("failed to materialize skill %s: %w", name, stepErr)
		}
	}
	return nil
}
