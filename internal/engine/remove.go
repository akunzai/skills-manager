package engine

import (
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
	// UntrackedDirectory is a real directory on the Scope skills directory
	// that Config does not declare. Retire keeps it; rm removes it only once
	// the user has confirmed.
	UntrackedDirectory bool
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

// UntrackedDirectories names the real directories Config does not declare
// that applying the plan would remove, which the user must confirm first.
func (p RemovePlan) UntrackedDirectories() []string {
	var names []string
	for _, item := range p.Skills {
		if item.UntrackedDirectory {
			names = append(names, item.Name)
		}
	}
	return names
}

// RemoveSkillResult is what ApplyRemovePlan did for one Skill: whether Config
// declared it, whether its Scope copy was there to remove, and what Retire
// did to it.
type RemoveSkillResult struct {
	RetiredSkill
	RemovedFromConfig bool
	MasterExisted     bool
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

// NotFullyRemoved names the Skills Retire could not take fully out of the
// Scope, in the order removed.
func (r RemoveResult) NotFullyRemoved() []string {
	var names []string
	for _, s := range r.Skills {
		if s.Err() != nil {
			names = append(names, s.Name)
		}
	}
	return names
}

// BuildRemovePlan records whether each name is in Config and whether its
// master Skill exists. It does not look at leftover empty agent dirs.
func BuildRemovePlan(cfg *config.Config, skillsDir string, names []string) RemovePlan {
	plan := RemovePlan{Skills: make([]RemoveItem, 0, len(names))}
	for _, name := range names {
		if name == "" {
			continue
		}
		kind, _, inConfig := config.FindSkillSource(cfg, name)
		info, err := os.Lstat(filepath.Join(skillsDir, name))
		plan.Skills = append(plan.Skills, RemoveItem{
			Name:               name,
			InConfig:           inConfig,
			Remote:             kind == config.SkillRemote,
			MasterExists:       err == nil,
			UntrackedDirectory: err == nil && !inConfig && info.IsDir(),
		})
	}
	return plan
}

// ApplyRemovePlan Retires each Skill, then removes the Untracked directories
// among them, which the caller has had the user confirm. A Config that cannot
// be saved is the error, with nothing on disk changed; everything Retire could
// not remove is in the result.
func ApplyRemovePlan(plan RemovePlan, cfg *config.Config, configPath, skillsDir string) (RemoveResult, error) {
	names := make([]string, len(plan.Skills))
	for i, item := range plan.Skills {
		names[i] = item.Name
	}
	baselines := OpenBaselines(skillsDir)
	retired, err := Retire(cfg, configPath, skillsDir, names, baselines)
	if err != nil {
		return RemoveResult{}, err
	}
	result := RemoveResult{Skills: make([]RemoveSkillResult, len(plan.Skills))}
	for i, item := range plan.Skills {
		skill := retired[i]
		if item.UntrackedDirectory && skill.CopyKept {
			skill.CopyKept = false
			skill.CopyErr = RemoveAll(skill.CopyPath)
			skill.CopyRemoved = skill.CopyErr == nil
		}
		result.Skills[i] = RemoveSkillResult{RetiredSkill: skill, RemovedFromConfig: skill.Undeclared, MasterExisted: item.MasterExists}
	}
	switch baselines.Verdict(plan.needsBaselines()) {
	case StateWarn:
		result.StateWarning = baselines.Err().Error()
	case StateFail:
		result.StateError = baselines.Err().Error()
	}
	return result, nil
}
