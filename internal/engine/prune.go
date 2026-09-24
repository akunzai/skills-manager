package engine

import (
	"cmp"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// PrunePlan describes managed filesystem entries that no longer match config.
type PrunePlan struct {
	UntrackedSkills []string
	UntrackedDirs   []string
	Unconfigured    []ManagedAgentPath
	StateSkills     []string
	// StateError is why the Scope state could not be read. Its stale
	// Baselines are left alone; everything else is still pruned.
	StateError string
}

func (p PrunePlan) AllUntracked() []string {
	return append(slices.Clone(p.UntrackedSkills), p.UntrackedDirs...)
}

// PruneFailure identifies a planned path that could not be removed.
type PruneFailure struct {
	Path string
	Err  error
}

// PruneResult records what happened while applying a plan.
type PruneResult struct {
	RemovedSkills []string
	RemovedLinks  []ManagedAgentPath
	SkippedLinks  []ManagedAgentPath
	Failures      []PruneFailure
}

// BuildPrunePlan finds untracked master skills and managed links that are no
// longer selected by the current configuration. Agent Availability paths are
// selected only when they are managed links into the master skills directory.
// Untracked real directories on the master are included so a TTY prune can
// offer them; callers that skip confirmation must omit those from apply.
func BuildPrunePlan(cfg *config.Config, skillsDir string, includeSkills, includeConfiguredLinks bool) (PrunePlan, error) {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	if skillsDir == "" {
		skillsDir = models.DefaultSkillsDir()
	}

	inv, err := LoadInventory(cfg, skillsDir)
	if err != nil {
		return PrunePlan{}, err
	}
	plan := PrunePlan{}
	baselines := OpenBaselines(skillsDir)
	if err := baselines.Err(); err != nil {
		plan.StateError = err.Error()
	} else {
		plan.StateSkills = baselines.Stale(cfg)
	}
	if includeSkills {
		plan.UntrackedSkills = inv.UntrackedLinks()
		plan.UntrackedDirs = inv.Untracked()
	}

	if includeSkills || includeConfiguredLinks {
		// The Agent directory observation is the same answer Doctor reports:
		// Unexpected paths of declared Skills and leftover occupancy, never
		// the same path in both.
		observation := NewAvailability(cfg, skillsDir).ObserveAgentDirs()
		leftover := observation.Leftover.WithoutEmpty()
		if includeConfiguredLinks {
			plan.Unconfigured = append(plan.Unconfigured, observation.Unexpected...)
		} else {
			leftover = leftover.ForSkills(append(inv.Untracked(), inv.UntrackedLinks()...))
		}
		for _, path := range leftover.Paths {
			plan.Unconfigured = append(plan.Unconfigured, path.ManagedAgentPath)
		}
	}

	slices.Sort(plan.UntrackedSkills)
	slices.Sort(plan.UntrackedDirs)
	slices.Sort(plan.StateSkills)
	slices.SortFunc(plan.Unconfigured, func(a, b ManagedAgentPath) int { return cmp.Compare(a.Path, b.Path) })
	return plan, nil
}

// ApplyPrunePlan removes every planned entry, continuing after failures. Each
// managed link is revalidated immediately before removal, since it can change
// while an interactive confirmation prompt is open.
func ApplyPrunePlan(plan PrunePlan, skillsDir string) (PruneResult, error) {
	if skillsDir == "" {
		skillsDir = models.DefaultSkillsDir()
	}
	result := PruneResult{}
	var errs []error
	for _, link := range plan.Unconfigured {
		managed, err := removeManagedSkillPath(link.Path, link.Skill, skillsDir)
		if !managed {
			result.SkippedLinks = append(result.SkippedLinks, link)
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			result.Failures = append(result.Failures, PruneFailure{Path: link.Path, Err: err})
			errs = append(errs, err)
			continue
		}
		result.RemovedLinks = append(result.RemovedLinks, link)
	}
	for _, skill := range plan.UntrackedSkills {
		path := filepath.Join(skillsDir, skill)
		if err := RemoveAll(path); err != nil {
			result.Failures = append(result.Failures, PruneFailure{Path: path, Err: err})
			errs = append(errs, err)
			continue
		}
		result.RemovedSkills = append(result.RemovedSkills, skill)
	}
	if len(plan.StateSkills) > 0 {
		baselines := OpenBaselines(skillsDir)
		if err := cmp.Or(baselines.Err(), baselines.Forget(plan.StateSkills...)); err != nil {
			errs = append(errs, err)
		}
	}
	return result, errors.Join(errs...)
}
