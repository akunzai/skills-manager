package engine

import (
	"cmp"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// PruneLink is a managed agent link selected for removal.
type PruneLink struct {
	Agent string
	Path  string
}

// PrunePlan describes managed filesystem entries that no longer match config.
type PrunePlan struct {
	UntrackedSkills []string
	UntrackedDirs   []string
	Unconfigured    []PruneLink
	StateSkills     []string
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
	RemovedLinks  []PruneLink
	SkippedLinks  []PruneLink
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
	store, err := NewScopeStateStore(skillsDir)
	if err != nil {
		return PrunePlan{}, err
	}
	state, err := store.Load()
	if err != nil {
		return PrunePlan{}, err
	}
	for name := range state.Skills {
		if _, _, declared := config.FindSkillSource(cfg, name); !declared {
			plan.StateSkills = append(plan.StateSkills, name)
		}
	}
	orphans := make(map[string]struct{})
	for _, name := range append(slices.Clone(inv.Untracked()), inv.UntrackedLinks()...) {
		orphans[name] = struct{}{}
	}
	if includeSkills {
		plan.UntrackedSkills = inv.UntrackedLinks()
		plan.UntrackedDirs = inv.Untracked()
	}

	if includeSkills || includeConfiguredLinks {
		availability := NewAvailability(cfg, skillsDir)
		links := make(map[string]PruneLink)
		if includeConfiguredLinks {
			agentDirs := models.GetAgentsForSkillsDir(skillsDir)
			observeUnexpected := func(name string) {
				for _, agent := range availability.ObserveAvailability(name).Unexpected {
					path := filepath.Join(agentDirs[agent], name)
					links[path] = PruneLink{Agent: agent, Path: path}
				}
			}
			for _, item := range inv.declaredPresent() {
				observeUnexpected(item.Name)
			}
			for _, name := range inv.Missing() {
				observeUnexpected(name)
			}
			for _, illegal := range inv.IllegalLocal() {
				observeUnexpected(illegal.Name)
			}
		}
		leftover := availability.ObserveLeftover().WithoutEmpty()
		if !includeConfiguredLinks {
			leftover = leftover.ForSkills(slices.Collect(maps.Keys(orphans)))
		}
		for _, path := range leftover.Paths {
			links[path.Path] = PruneLink{Agent: path.Agent, Path: path.Path}
		}
		plan.Unconfigured = append(plan.Unconfigured, slices.Collect(maps.Values(links))...)
	}

	slices.Sort(plan.UntrackedSkills)
	slices.Sort(plan.UntrackedDirs)
	slices.Sort(plan.StateSkills)
	slices.SortFunc(plan.Unconfigured, func(a, b PruneLink) int { return cmp.Compare(a.Path, b.Path) })
	return plan, nil
}

// ApplyPrunePlan removes every planned entry, continuing after failures. Each
// managed link is revalidated immediately before removal, since it can change
// while an interactive confirmation prompt is open.
func ApplyPrunePlan(plan PrunePlan, skillsDir string) (PruneResult, error) {
	result := PruneResult{}
	var errs []error
	for _, link := range plan.Unconfigured {
		managed, err := removeManagedSkillPath(link.Path, filepath.Base(link.Path), skillsDir)
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
	store, stateErr := NewScopeStateStore(skillsDir)
	if stateErr != nil {
		errs = append(errs, stateErr)
	} else {
		for _, skill := range plan.StateSkills {
			if err := store.DeleteSkill(skill); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return result, errors.Join(errs...)
}
