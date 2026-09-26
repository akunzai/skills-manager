package engine

import (
	"errors"
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
	CopyPath    string
	// CopyRemoved is whether the Scope copy is gone, including one that was
	// already missing.
	CopyRemoved bool
	CopyErr     error
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

// Retire takes Skills cfg no longer declares out of the Scope, the inverse of
// Materialize. cfg is already modified; Retire saves it to configPath first,
// and a failed save returns the error with nothing on disk changed. Only then
// does it remove each named Skill's Availability links, its Scope copy, and
// its Baseline, reporting each Skill's result in the order of names. A failure
// on one does not stop the rest: an interrupted Retire leaves Config saved, so
// what remains is Leftover occupancy and Untracked content, not a declared
// Skill Sync never wrote.
func Retire(cfg *config.Config, configPath, skillsDir string, names []string, baselines *Baselines) ([]RetiredSkill, error) {
	if err := config.SaveConfig(cfg, configPath); err != nil {
		return nil, err
	}
	retired := make([]RetiredSkill, len(names))
	index := make(map[string]int, len(names))
	for i, name := range names {
		retired[i] = RetiredSkill{Name: name, CopyPath: filepath.Join(skillsDir, name)}
		index[name] = i
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
		if skill.CopyErr = RemoveAll(skill.CopyPath); skill.CopyErr == nil {
			skill.CopyRemoved = true
		}
		skill.BaselineErr = baselines.Forget(skill.Name)
	}
	return retired, nil
}
