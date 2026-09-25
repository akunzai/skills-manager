package engine

import (
	"cmp"
	"errors"
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
	// EmptyAgentDirs are leftover empty Agent directories the current
	// Availability policy does not select (see CONTEXT.md's Leftover
	// occupancy). Populated only when BuildPrunePlan is asked to include
	// configured links, the same condition Unconfigured uses.
	EmptyAgentDirs []AgentDir
	StateSkills    []string
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
	RemovedSkills    []string
	RemovedLinks     []ManagedAgentPath
	SkippedLinks     []ManagedAgentPath
	RemovedEmptyDirs []AgentDir
	SkippedEmptyDirs []AgentDir
	// ForgottenBaselines are the stale Baselines removed from the Scope state.
	ForgottenBaselines []string
	Failures           []PruneFailure
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
	if includeSkills {
		// A stale Baseline is a record about a Skill, so it goes with the
		// skills directory, not with Agent directory links.
		baselines := OpenBaselines(skillsDir)
		if err := baselines.Err(); err != nil {
			plan.StateError = err.Error()
		} else {
			plan.StateSkills = baselines.Stale(cfg)
		}
		plan.UntrackedSkills = inv.UntrackedLinks()
		plan.UntrackedDirs = inv.Untracked()
	}

	if includeSkills || includeConfiguredLinks {
		// The Agent directory observation is the same answer Doctor reports:
		// Unexpected paths of declared Skills and leftover occupancy, never
		// the same path in both.
		observation := NewAvailability(cfg, skillsDir).ObserveOccupancy()
		leftover := observation.Leftover.WithoutEmpty()
		if includeConfiguredLinks {
			plan.Unconfigured = append(plan.Unconfigured, observation.Unexpected...)
			plan.EmptyAgentDirs = slices.Clone(observation.Leftover.Empty)
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
	slices.SortFunc(plan.EmptyAgentDirs, func(a, b AgentDir) int { return cmp.Compare(a.Name, b.Name) })
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
	for i, repair := range removeManagedPaths(skillsDir, plan.Unconfigured) {
		link := plan.Unconfigured[i]
		switch repair.Status {
		case RepairSkipped:
			result.SkippedLinks = append(result.SkippedLinks, link)
		case RepairFailed:
			result.Failures = append(result.Failures, PruneFailure{Path: link.Path, Err: repair.Err})
			errs = append(errs, repair.Err)
		default:
			result.RemovedLinks = append(result.RemovedLinks, link)
		}
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
	for i, repair := range removeEmptyAgentDirs(skillsDir, plan.EmptyAgentDirs) {
		dir := plan.EmptyAgentDirs[i]
		switch repair.Status {
		case RepairSkipped:
			result.SkippedEmptyDirs = append(result.SkippedEmptyDirs, dir)
		case RepairFailed:
			result.Failures = append(result.Failures, PruneFailure{Path: dir.Dir, Err: repair.Err})
			errs = append(errs, repair.Err)
		default:
			result.RemovedEmptyDirs = append(result.RemovedEmptyDirs, dir)
		}
	}
	if len(plan.StateSkills) > 0 {
		baselines := OpenBaselines(skillsDir)
		if err := cmp.Or(baselines.Err(), baselines.Forget(plan.StateSkills...)); err != nil {
			errs = append(errs, err)
		} else {
			result.ForgottenBaselines = slices.Clone(plan.StateSkills)
		}
	}
	return result, errors.Join(errs...)
}
