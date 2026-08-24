package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/akunzai/skills-manager/internal/config"
)

type SkillFreshnessStatus string

const (
	SkillInSync               SkillFreshnessStatus = "in_sync"
	SkillMissing              SkillFreshnessStatus = "missing"
	SkillCacheUpdateAvailable SkillFreshnessStatus = "cache_update_available"
	SkillLocalDrift           SkillFreshnessStatus = "local_drift"
	SkillUnknownBaseline      SkillFreshnessStatus = "unknown_baseline"
	SkillUnverified           SkillFreshnessStatus = "unverified"
	SkillError                SkillFreshnessStatus = "error"
)

type ContentChanges struct {
	Added    []string `json:"added,omitempty"`
	Removed  []string `json:"removed,omitempty"`
	Modified []string `json:"modified,omitempty"`
}

type SkillFreshness struct {
	Name             string               `json:"name"`
	Source           string               `json:"source"`
	Subpath          string               `json:"subpath"`
	ScopePath        string               `json:"scope_path"`
	CachePath        string               `json:"cache_path"`
	Status           SkillFreshnessStatus `json:"status"`
	BaselineRecorded bool                 `json:"baseline_recorded"`
	Changes          ContentChanges       `json:"changes,omitempty"`
	CacheDigests     map[string]string    `json:"-"`
	ScopeDigests     map[string]string    `json:"-"`
	Error            string               `json:"error,omitempty"`
}

type RepositoryFreshness struct {
	Source    string           `json:"source"`
	URL       string           `json:"url"`
	Branch    string           `json:"branch"`
	CachePath string           `json:"cache_path"`
	CacheSHA  string           `json:"cache_sha"`
	Skills    []SkillFreshness `json:"skills"`
}

type ScopeFreshnessPlan struct {
	Repositories []RepositoryFreshness `json:"repositories"`
	State        ScopeState            `json:"-"`
	StateStore   *ScopeStateStore      `json:"-"`
	StateError   string                `json:"state_error,omitempty"`
}

func PlanScopeFreshness(cfg *config.Config, skillsDir, cacheDir string) (*ScopeFreshnessPlan, error) {
	store, err := NewScopeStateStore(skillsDir)
	if err != nil {
		return nil, err
	}
	state, stateErr := store.Load()
	if stateErr != nil {
		state = store.emptyState()
	}
	plan := &ScopeFreshnessPlan{State: state, StateStore: store}
	if stateErr != nil {
		plan.StateError = stateErr.Error()
	}
	sources := make([]string, 0, len(cfg.Remote))
	for source := range cfg.Remote {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	for _, source := range sources {
		repoInfo := cfg.Remote[source]
		cache := resolveCacheRepo(source, repoInfo.URL, repoInfo.Branch, cacheDir)
		repository := RepositoryFreshness{Source: source, URL: cache.URL, Branch: cache.Branch, CachePath: cache.Dir, CacheSHA: GetLocalRepoCommit(cache.Dir)}
		for _, name := range sortedSkillKeys(repoInfo.Skills) {
			skill := classifyRemoteSkill(source, name, repoInfo.Skills[name], cache.Dir, skillsDir, state.Skills[name])
			if repository.CacheSHA == "" {
				skill.Status = SkillUnverified
				skill.BaselineRecorded = false
				skill.CacheDigests = nil
			}
			if stateErr != nil && skill.Status != SkillUnverified && skill.Status != SkillMissing && skill.Status != SkillError {
				skill.Status = SkillUnknownBaseline
				skill.BaselineRecorded = false
			}
			repository.Skills = append(repository.Skills, skill)
		}
		plan.Repositories = append(plan.Repositories, repository)
	}
	return plan, nil
}

func classifyRemoteSkill(source, name, subpath, cacheDir, skillsDir string, applied AppliedSkillState) SkillFreshness {
	result := SkillFreshness{Name: name, Source: source, Subpath: subpath, ScopePath: filepath.Join(skillsDir, name), CachePath: filepath.Join(cacheDir, filepath.FromSlash(subpath)), BaselineRecorded: applied.Source != ""}
	cacheDigests, err := DigestSkillContent(result.CachePath)
	if errors.Is(err, os.ErrNotExist) || os.IsNotExist(rootPathError(err)) {
		result.Status = SkillUnverified
		return result
	}
	if err != nil {
		result.Status, result.Error = SkillError, err.Error()
		return result
	}
	result.CacheDigests = cacheDigests
	scopeDigests, err := DigestSkillContent(result.ScopePath)
	if errors.Is(err, os.ErrNotExist) || os.IsNotExist(rootPathError(err)) {
		result.Status = SkillMissing
		return result
	}
	if err != nil {
		result.Status, result.Error = SkillError, err.Error()
		return result
	}
	result.ScopeDigests = scopeDigests
	result.Changes = compareDigestMaps(scopeDigests, cacheDigests)
	if reflect.DeepEqual(scopeDigests, cacheDigests) {
		result.Status = SkillInSync
	} else if !result.BaselineRecorded || applied.CacheIdentity != cacheDir {
		result.Status = SkillUnknownBaseline
	} else if applied.Source == source && reflect.DeepEqual(scopeDigests, applied.ContentDigests) {
		result.Status = SkillCacheUpdateAvailable
	} else {
		result.Status = SkillLocalDrift
	}
	return result
}

func rootPathError(err error) error {
	for err != nil {
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			return pathErr.Err
		}
		err = errors.Unwrap(err)
	}
	return nil
}

func compareDigestMaps(current, desired map[string]string) ContentChanges {
	var changes ContentChanges
	for path, digest := range desired {
		currentDigest, exists := current[path]
		if !exists {
			changes.Added = append(changes.Added, path)
		} else if currentDigest != digest {
			changes.Modified = append(changes.Modified, path)
		}
	}
	for path := range current {
		if _, exists := desired[path]; !exists {
			changes.Removed = append(changes.Removed, path)
		}
	}
	sort.Strings(changes.Added)
	sort.Strings(changes.Removed)
	sort.Strings(changes.Modified)
	return changes
}

func (skill SkillFreshness) appliedState(cacheIdentity, commit string) AppliedSkillState {
	return AppliedSkillState{Source: skill.Source, CacheIdentity: cacheIdentity, AppliedCommit: commit, ContentDigests: skill.CacheDigests}
}

func (skill SkillFreshness) validateCache() error {
	if skill.Status == SkillUnverified {
		return fmt.Errorf("Cache missing for Source %s; run 'skills update' first", skill.Source)
	}
	if skill.Status == SkillError {
		return errors.New(skill.Error)
	}
	return nil
}
