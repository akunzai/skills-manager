package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/akunzai/skills-manager/internal/config"
)

// RemoveItem is one Skill selected for removal.
type RemoveItem struct {
	Name         string
	InConfig     bool
	MasterExists bool
	MasterPath   string
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
		_, _, inConfig := config.FindSkillSource(cfg, name)
		path := filepath.Join(skillsDir, name)
		_, err := os.Lstat(path)
		plan.Skills = append(plan.Skills, RemoveItem{
			Name:         name,
			InConfig:     inConfig,
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

	for i, item := range plan.Skills {
		unlinked := RemoveAgentSymlinks(item.Name, skillsDir)
		result.Skills[i].Unlinked = unlinked

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
	return result, result.Err()
}
