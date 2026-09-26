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
	// EmptyAgentDirs are leftover empty Agent directories the current
	// Availability policy does not select (see CONTEXT.md's Leftover
	// occupancy). Populated only when BuildPrunePlan is asked to include
	// configured links, the same condition Unconfigured uses.
	EmptyAgentDirs []AgentDir
	StateSkills    []string
	// StateError is why the Scope state could not be read. Its stale
	// Baselines are left alone; everything else is still pruned.
	StateError string
	// approvedDirs are the UntrackedDirs the user selected. An untracked real
	// directory is the user's own content, so only Select sets this and
	// ApplyPrunePlan removes no other real directory.
	approvedDirs []string
}

func (p PrunePlan) AllUntracked() []string {
	return append(slices.Clone(p.UntrackedSkills), p.UntrackedDirs...)
}

// Empty reports whether applying the plan would remove nothing at all. An
// untracked real directory Select has not approved is never removed, so it
// does not count.
func (p PrunePlan) Empty() bool {
	return len(p.UntrackedSkills) == 0 && len(p.approvedDirs) == 0 && len(p.Unconfigured) == 0 && len(p.EmptyAgentDirs) == 0 && len(p.StateSkills) == 0
}

// PruneSelection is what the user chose from a PrunePlan: untracked master
// Skills by name, managed links by path, empty Agent directories by path, and
// stale Baselines by Skill name.
type PruneSelection struct {
	Masters   []string
	Links     []string
	EmptyDirs []string
	Baselines []string
}

// Select narrows the plan to selection. A selected untracked master Skill
// takes its managed links with it, and a selected untracked real directory is
// approved for removal; one not selected stays, whatever else is chosen.
func (p PrunePlan) Select(selection PruneSelection) PrunePlan {
	masters := setOf(selection.Masters)
	links := setOf(selection.Links)
	emptyDirs := setOf(selection.EmptyDirs)
	baselines := setOf(selection.Baselines)
	var selected PrunePlan
	for _, skill := range p.UntrackedSkills {
		if masters[skill] {
			selected.UntrackedSkills = append(selected.UntrackedSkills, skill)
		}
	}
	for _, dir := range p.UntrackedDirs {
		if masters[dir] {
			selected.approvedDirs = append(selected.approvedDirs, dir)
		}
	}
	for _, link := range p.Unconfigured {
		if links[link.Path] || masters[link.Skill] {
			selected.Unconfigured = append(selected.Unconfigured, link)
		}
	}
	for _, dir := range p.EmptyAgentDirs {
		if emptyDirs[dir.Dir] {
			selected.EmptyAgentDirs = append(selected.EmptyAgentDirs, dir)
		}
	}
	for _, name := range p.StateSkills {
		if baselines[name] {
			selected.StateSkills = append(selected.StateSkills, name)
		}
	}
	return selected
}

func setOf(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

// PruneFailure identifies a planned path that could not be removed.
type PruneFailure struct {
	Path string
	Err  error
}

// PruneResult records what happened while applying a plan.
type PruneResult struct {
	RemovedSkills []string
	// SkippedSkills are untracked entries listed for removal as links that are
	// not links: only Select can approve removing a real directory.
	SkippedSkills    []string
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
// Untracked real directories on the master are listed so a TTY prune can
// offer them; only Select can approve one for removal.
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
	remove := func(skill string) {
		path := filepath.Join(skillsDir, skill)
		if err := RemoveAll(path); err != nil {
			result.Failures = append(result.Failures, PruneFailure{Path: path, Err: err})
			errs = append(errs, err)
			return
		}
		result.RemovedSkills = append(result.RemovedSkills, skill)
	}
	for _, skill := range plan.UntrackedSkills {
		// Checked again right before removal: a real directory here, listed
		// by a caller or put in place since planning, was never approved.
		if info, err := os.Lstat(filepath.Join(skillsDir, skill)); err == nil && info.Mode()&os.ModeSymlink == 0 {
			result.SkippedSkills = append(result.SkippedSkills, skill)
			continue
		}
		remove(skill)
	}
	for _, dir := range plan.approvedDirs {
		remove(dir)
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
