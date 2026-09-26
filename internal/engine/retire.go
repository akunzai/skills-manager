package engine

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/akunzai/skills-manager/internal/config"
)

// RetiredSkill is what Retire did to one Skill.
type RetiredSkill struct {
	Name string
	// Unlinked are the Availability paths removed.
	Unlinked []ManagedAgentPath
	// FailedLinks are the Availability paths that could not be removed.
	FailedLinks []FailedLink
	// Undeclared is whether Retire removed anything of the Skill's from
	// Config: its declaration or an Availability override.
	Undeclared bool
	CopyPath   string
	// CopyRemoved is whether the Scope copy is gone, including one that was
	// already missing.
	CopyRemoved bool
	// CopyKept is a real directory Retire left on CopyPath because
	// Materialize never wrote it: the Source of a local Skill, or content
	// Config did not declare.
	CopyKept bool
	CopyErr  error
	// BaselineErr is why the Baseline could not be forgotten, or nil when it
	// was or none was recorded. ErrNotRecorded is the Scope state's verdict,
	// which the caller decides (see Baselines.Verdict).
	BaselineErr error
}

// FailedLink is an Availability path Retire could not remove.
type FailedLink struct {
	ManagedAgentPath
	Err error
}

// Err joins what stopped the Skill being fully retired. An unreadable Scope
// state is not among them: it is the Scope's verdict, counted once by the
// caller, not a failure of each Skill.
func (s RetiredSkill) Err() error {
	var errs []error
	for _, link := range s.FailedLinks {
		errs = append(errs, link.Err)
	}
	errs = append(errs, s.CopyErr)
	if !errors.Is(s.BaselineErr, ErrNotRecorded) {
		errs = append(errs, s.BaselineErr)
	}
	return errors.Join(errs...)
}

// Retire takes Skills out of the Scope, the inverse of Materialize. It reads
// how cfg declares each named Skill, undeclares it, and saves cfg to
// configPath; a failed save returns the error with nothing on disk changed.
// Only then does it remove each Skill's Availability links, the Scope copy
// Materialize wrote for its kind, and its Baseline, reporting each Skill's
// result in the order of names. A local Skill's Scope path is only ever a
// link, so a real directory there, such as an Illegal-local Source, is kept,
// as is one Config never declared. A failure on one Skill does not stop the
// rest: an interrupted Retire leaves Config saved, so what remains is Leftover
// occupancy and Untracked content, not a declared Skill Sync never wrote.
func Retire(cfg *config.Config, configPath, skillsDir string, names []string, baselines *Baselines) ([]RetiredSkill, error) {
	retired := make([]RetiredSkill, len(names))
	index := make(map[string]int, len(names))
	wroteCopy := make([]bool, len(names))
	for i, name := range names {
		kind, _, _ := config.FindSkillSource(cfg, name)
		wroteCopy[i] = kind == config.SkillRemote || kind == config.SkillCommand
		retired[i] = RetiredSkill{Name: name, Undeclared: config.RemoveSkillEntry(cfg, name), CopyPath: filepath.Join(skillsDir, name)}
		index[name] = i
	}
	if err := config.SaveConfig(cfg, configPath); err != nil {
		return nil, err
	}

	availability := NewAvailability(cfg, skillsDir)
	links := availability.RemoveLeftover(availability.ObserveOccupancy().Leftover.ForSkills(names).WithoutEmpty())
	for _, link := range links.Paths {
		skill := &retired[index[link.Skill]]
		switch link.Repair.Status {
		case RepairSucceeded:
			skill.Unlinked = append(skill.Unlinked, link.ManagedAgentPath)
		case RepairFailed:
			skill.FailedLinks = append(skill.FailedLinks, FailedLink{ManagedAgentPath: link.ManagedAgentPath, Err: link.Repair.Err})
		}
	}
	for i := range retired {
		skill := &retired[i]
		if wroteCopy[i] {
			skill.CopyErr = RemoveAll(skill.CopyPath)
		} else {
			skill.CopyKept, skill.CopyErr = removeUnlessDirectory(skill.CopyPath)
		}
		skill.CopyRemoved = skill.CopyErr == nil && !skill.CopyKept
		skill.BaselineErr = baselines.Forget(skill.Name)
	}
	return retired, nil
}

// removeUnlessDirectory removes path when it is a link or a file, and keeps
// a real directory, reporting that it did.
func removeUnlessDirectory(path string) (kept bool, err error) {
	info, err := os.Lstat(path)
	switch {
	case os.IsNotExist(err):
		return false, nil
	case err != nil:
		return false, err
	case info.IsDir():
		return true, nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	return false, nil
}
