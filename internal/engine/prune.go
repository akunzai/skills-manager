package engine

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	Unconfigured    []PruneLink
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
// longer selected by the current configuration. It never selects user-owned
// directories or links that point somewhere other than the master skills dir.
func BuildPrunePlan(cfg *config.Config, skillsDir string, includeSkills, includeConfiguredLinks bool) (PrunePlan, error) {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	if skillsDir == "" {
		skillsDir = models.DefaultSkillsDir()
	}

	configured := make(map[string]string)
	for source, repo := range cfg.Remote {
		for skill := range repo.Skills {
			configured[skill] = source
		}
	}
	for skill := range cfg.Local {
		configured[skill] = "local"
	}

	plan := PrunePlan{}
	orphans := make(map[string]struct{})
	entries, err := os.ReadDir(skillsDir)
	if err != nil && !os.IsNotExist(err) {
		return plan, err
	}
	if err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if _, ok := configured[name]; !ok {
				orphans[name] = struct{}{}
				if includeSkills {
					plan.UntrackedSkills = append(plan.UntrackedSkills, name)
				}
			}
		}
	}

	if includeSkills || includeConfiguredLinks {
		links := make(map[string]PruneLink)
		addManagedLinks := func(skill string, predicate func(string) bool) {
			for agent, dir := range pruneAgentDirs(skillsDir) {
				if !predicate(agent) {
					continue
				}
				path := filepath.Join(dir, skill)
				if IsManagedSkillLink(path, skill, skillsDir) {
					links[path] = PruneLink{Agent: agent, Path: path}
				}
			}
		}
		if includeConfiguredLinks {
			for skill, source := range configured {
				targets := make(map[string]bool)
				for _, agent := range GetTargetAgentsForSkill(skill, source, cfg, skillsDir) {
					targets[agent] = true
				}
				addManagedLinks(skill, func(agent string) bool { return !targets[agent] })
			}
		}
		for skill := range orphans {
			addManagedLinks(skill, func(string) bool { return true })
		}
		for _, link := range links {
			plan.Unconfigured = append(plan.Unconfigured, link)
		}
	}

	sort.Strings(plan.UntrackedSkills)
	sort.Slice(plan.Unconfigured, func(i, j int) bool { return plan.Unconfigured[i].Path < plan.Unconfigured[j].Path })
	return plan, nil
}

func pruneAgentDirs(skillsDir string) map[string]string {
	dirs := models.GetAgentsForSkillsDir(skillsDir)
	for agent, dir := range models.GetUniversalAgentSkillDirs(skillsDir) {
		if _, exists := dirs[agent]; !exists {
			dirs[agent] = dir
		}
	}
	return dirs
}

// ApplyPrunePlan removes every planned entry, continuing after failures. Each
// managed link is revalidated immediately before removal, since it can change
// while an interactive confirmation prompt is open.
func ApplyPrunePlan(plan PrunePlan, skillsDir string) (PruneResult, error) {
	result := PruneResult{}
	var errs []error
	for _, link := range plan.Unconfigured {
		if !IsManagedSkillLink(link.Path, filepath.Base(link.Path), skillsDir) {
			result.SkippedLinks = append(result.SkippedLinks, link)
			continue
		}
		if err := os.Remove(link.Path); err != nil && !os.IsNotExist(err) {
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
	return result, errors.Join(errs...)
}
