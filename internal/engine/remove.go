package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/akunzai/skills-manager/internal/config"
)

// RemoveItem is one Skill selected for removal.
type RemoveItem struct {
	Name     string
	InConfig bool
	// Remote is whether Config declares the Skill from a remote Source, the
	// only kind with a Baseline to forget.
	Remote       bool
	MasterExists bool
	MasterPath   string
}

// needsBaselines reports whether removing these Skills forgets a Baseline. A
// Skill Config does not declare may still have a stale one, so it counts.
func (p RemovePlan) needsBaselines() bool {
	return slices.ContainsFunc(p.Skills, func(item RemoveItem) bool { return item.Remote || !item.InConfig })
}

// RemovePlan is the Skills rm will drop from Config then from disk.
type RemovePlan struct {
	Skills []RemoveItem
}

// RemoveSkillResult is what ApplyRemovePlan did for one Skill.
type RemoveSkillResult struct {
	Name              string
	RemovedFromConfig bool
	Unlinked          []string
	RemovedMaster     bool
	MasterPath        string
	MasterErr         error
}

// RemoveResult is the observable outcome of applying a RemovePlan.
type RemoveResult struct {
	Skills []RemoveSkillResult
	// StateError is why the Scope state could not be read. The Skills are
	// removed but their Baselines are not forgotten.
	StateError string
	// StateWarning is why the Scope state could not be read when no removed
	// Skill had a Baseline to forget: a warning, not a failure (ADR-0002).
	StateWarning string
}

func (r RemoveResult) Err() error {
	var errs []error
	for _, s := range r.Skills {
		if s.MasterErr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Name, s.MasterErr))
		}
	}
	return errors.Join(errs...)
}

// BuildRemovePlan records whether each name is in Config and whether its
// master Skill exists. It does not look at leftover empty agent dirs.
func BuildRemovePlan(cfg *config.Config, skillsDir string, names []string) RemovePlan {
	plan := RemovePlan{Skills: make([]RemoveItem, 0, len(names))}
	for _, name := range names {
		if name == "" {
			continue
		}
		category, _, inConfig := config.FindSkillSource(cfg, name)
		path := filepath.Join(skillsDir, name)
		_, err := os.Lstat(path)
		plan.Skills = append(plan.Skills, RemoveItem{
			Name:         name,
			InConfig:     inConfig,
			Remote:       category == "remote",
			MasterExists: err == nil,
			MasterPath:   path,
		})
	}
	return plan
}

// ApplyRemovePlan drops each Skill from Config and saves, then unlinks
// Availability and removes the master directory. Master removal failures are
// recorded and joined; Config no longer declares those Skills.
func ApplyRemovePlan(plan RemovePlan, cfg *config.Config, configPath, skillsDir string) (RemoveResult, error) {
	result := RemoveResult{Skills: make([]RemoveSkillResult, 0, len(plan.Skills))}
	for _, item := range plan.Skills {
		step := RemoveSkillResult{Name: item.Name, MasterPath: item.MasterPath}
		step.RemovedFromConfig = config.RemoveSkillEntry(cfg, item.Name)
		result.Skills = append(result.Skills, step)
	}
	if err := config.SaveConfig(cfg, configPath); err != nil {
		return result, err
	}

	availability := NewAvailability(cfg, skillsDir)
	names := make([]string, 0, len(plan.Skills))
	for _, item := range plan.Skills {
		names = append(names, item.Name)
	}
	leftover := availability.RemoveLeftover(availability.ObserveOccupancy().Leftover.ForSkills(names).WithoutEmpty())
	removedBySkill := make(map[string][]string)
	for _, path := range leftover.Paths {
		if path.Repair.Status == RepairSucceeded {
			removedBySkill[path.Skill] = append(removedBySkill[path.Skill], path.Agent)
		}
	}
	for i, item := range plan.Skills {
		result.Skills[i].Unlinked = removedBySkill[item.Name]

		if _, err := os.Lstat(item.MasterPath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			result.Skills[i].MasterErr = err
			continue
		}
		if err := RemoveAll(item.MasterPath); err != nil {
			result.Skills[i].MasterErr = err
			continue
		}
		result.Skills[i].RemovedMaster = true
	}
	baselines := OpenBaselines(skillsDir)
	if stateErr := baselines.Err(); stateErr != nil && !plan.needsBaselines() {
		result.StateWarning = stateErr.Error()
		return result, result.Err()
	} else if stateErr != nil {
		result.StateError = stateErr.Error()
		return result, errors.Join(result.Err(), fmt.Errorf("removed Skills but did not forget their Baselines: %w", stateErr))
	}
	if err := baselines.Forget(names...); err != nil {
		return result, errors.Join(result.Err(), err)
	}
	return result, result.Err()
}
