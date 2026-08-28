package engine

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/akunzai/skills-manager/internal/config"
)

type SkillFreshnessStatus string

type RemoteFreshnessStatus string

const (
	RemoteUpToDate        RemoteFreshnessStatus = "up_to_date"
	RemoteUpdateAvailable RemoteFreshnessStatus = "update_available"
	RemoteNotCached       RemoteFreshnessStatus = "not_cached"
	RemoteError           RemoteFreshnessStatus = "error"
)

type FreshnessDispositionKind string

const (
	FreshnessNone         FreshnessDispositionKind = "none"
	FreshnessUpdate       FreshnessDispositionKind = "update"
	FreshnessSync         FreshnessDispositionKind = "sync"
	FreshnessProtectDrift FreshnessDispositionKind = "protect_drift"
	FreshnessInvestigate  FreshnessDispositionKind = "investigate"
)

type FreshnessDisposition struct {
	Kind   FreshnessDispositionKind `json:"kind"`
	Reason string                   `json:"reason"`
	Source string                   `json:"source,omitempty"`
	Skill  string                   `json:"skill,omitempty"`
}

type FreshnessRepository struct {
	Source       string                `json:"source"`
	URL          string                `json:"url"`
	Branch       string                `json:"branch"`
	RemoteStatus RemoteFreshnessStatus `json:"status"`
	LocalSHA     string                `json:"local_sha"`
	RemoteSHA    string                `json:"remote_sha"`
	CachePath    string                `json:"cache_path"`
	Error        string                `json:"error,omitempty"`
	Skills       []SkillFreshness      `json:"skills"`
}

type FreshnessSnapshot struct {
	Repositories []FreshnessRepository `json:"repositories"`
	StateError   string                `json:"state_error,omitempty"`
}

type FreshnessOptions struct {
	ObserveRemote bool
	ObserveScope  bool
	Workers       int
}

func (s FreshnessSnapshot) Dispositions() []FreshnessDisposition {
	byKind := make(map[FreshnessDispositionKind][]FreshnessDisposition)
	add := func(kind FreshnessDispositionKind, reason, source, skill string) {
		byKind[kind] = append(byKind[kind], FreshnessDisposition{Kind: kind, Reason: reason, Source: source, Skill: skill})
	}
	if s.StateError != "" {
		add(FreshnessInvestigate, "scope_state_error", "", "")
	}
	for _, repository := range s.Repositories {
		switch repository.RemoteStatus {
		case RemoteUpdateAvailable, RemoteNotCached:
			add(FreshnessUpdate, string(repository.RemoteStatus), repository.Source, "")
		case RemoteError:
			add(FreshnessUpdate, string(repository.RemoteStatus), repository.Source, "")
			add(FreshnessInvestigate, "remote_error", repository.Source, "")
		}
		for _, skill := range repository.Skills {
			switch skill.Status {
			case SkillMissing, SkillCacheUpdateAvailable, SkillUnknownBaseline, SkillUnverified:
				add(FreshnessSync, string(skill.Status), repository.Source, skill.Name)
			case SkillLocalDrift:
				add(FreshnessSync, string(skill.Status), repository.Source, skill.Name)
				add(FreshnessProtectDrift, string(skill.Status), repository.Source, skill.Name)
			case SkillError:
				add(FreshnessSync, string(skill.Status), repository.Source, skill.Name)
				add(FreshnessInvestigate, "skill_error", repository.Source, skill.Name)
			}
		}
	}
	if len(byKind) == 0 {
		return []FreshnessDisposition{{Kind: FreshnessNone, Reason: "no_action"}}
	}
	var dispositions []FreshnessDisposition
	for _, kind := range []FreshnessDispositionKind{FreshnessUpdate, FreshnessSync, FreshnessProtectDrift, FreshnessInvestigate} {
		dispositions = append(dispositions, byKind[kind]...)
	}
	return dispositions
}

func (s FreshnessSnapshot) DispositionKinds() []FreshnessDispositionKind {
	dispositions := s.Dispositions()
	seen := make(map[FreshnessDispositionKind]bool)
	kinds := make([]FreshnessDispositionKind, 0, len(dispositions))
	for _, disposition := range dispositions {
		if seen[disposition.Kind] {
			continue
		}
		seen[disposition.Kind] = true
		kinds = append(kinds, disposition.Kind)
	}
	return kinds
}

func (s FreshnessSnapshot) Fresh() bool {
	kinds := s.DispositionKinds()
	return len(kinds) == 1 && kinds[0] == FreshnessNone
}

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

func InspectFreshness(cfg *config.Config, skillsDir, cacheDir string, options FreshnessOptions) (*FreshnessSnapshot, error) {
	snapshot := &FreshnessSnapshot{}
	if options.ObserveRemote {
		snapshot.Repositories = observeRemoteFreshness(cfg.Remote, cacheDir, options.Workers)
	} else {
		for _, source := range slices.Sorted(maps.Keys(cfg.Remote)) {
			cache := resolveCacheRepo(source, cfg.Remote[source].URL, cfg.Remote[source].Branch, cacheDir)
			snapshot.Repositories = append(snapshot.Repositories, FreshnessRepository{
				Source: source, URL: cache.URL, Branch: cache.Branch, CachePath: cache.Dir, LocalSHA: GetLocalRepoCommit(cache.Dir),
			})
		}
	}
	if !options.ObserveScope {
		return snapshot, nil
	}
	return attachScopeObservations(snapshot, cfg, skillsDir)
}

func attachScopeObservations(snapshot *FreshnessSnapshot, cfg *config.Config, skillsDir string) (*FreshnessSnapshot, error) {
	store, err := NewScopeStateStore(skillsDir)
	if err != nil {
		return nil, err
	}
	state, stateErr := store.Load()
	if stateErr != nil {
		state = store.emptyState()
		snapshot.StateError = stateErr.Error()
	}
	for i := range snapshot.Repositories {
		source := snapshot.Repositories[i].Source
		repoInfo := cfg.Remote[source]
		cachePath := snapshot.Repositories[i].CachePath
		for _, name := range sortedSkillKeys(repoInfo.Skills) {
			skill := classifyRemoteSkill(source, name, repoInfo.Skills[name], cachePath, skillsDir, state.Skills[name])
			if snapshot.Repositories[i].LocalSHA == "" {
				skill.Status = SkillUnverified
				skill.BaselineRecorded = false
				skill.CacheDigests = nil
			}
			if stateErr != nil && skill.Status != SkillUnverified && skill.Status != SkillMissing && skill.Status != SkillError {
				skill.Status = SkillUnknownBaseline
				skill.BaselineRecorded = false
			}
			snapshot.Repositories[i].Skills = append(snapshot.Repositories[i].Skills, skill)
		}
	}
	return snapshot, nil
}

func observeRemoteFreshness(repositories map[string]config.RemoteRepo, cacheDir string, workers int) []FreshnessRepository {
	if workers <= 0 {
		workers = 8
	}
	type task struct {
		source string
		repo   config.RemoteRepo
	}
	tasks := make([]task, 0, len(repositories))
	for source, repo := range repositories {
		tasks = append(tasks, task{source, repo})
	}
	results := make([]FreshnessRepository, len(tasks))
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, current := range tasks {
		wg.Go(func() {
			sem <- struct{}{}
			results[i] = observeRemoteSource(current.source, current.repo, cacheDir)
			<-sem
		})
	}
	wg.Wait()
	slices.SortFunc(results, func(a, b FreshnessRepository) int {
		return cmp.Compare(strings.ToLower(a.Source), strings.ToLower(b.Source))
	})
	return results
}

var observeRemoteSource = func(source string, repo config.RemoteRepo, cacheDir string) FreshnessRepository {
	return newRemoteSource(nil, source, repo, cacheDir).ObserveFreshness()
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
		if pathErr, ok := errors.AsType[*os.PathError](err); ok {
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
	slices.Sort(changes.Added)
	slices.Sort(changes.Removed)
	slices.Sort(changes.Modified)
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
