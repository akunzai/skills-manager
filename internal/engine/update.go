package engine

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/akunzai/skills-manager/internal/config"
)

type UpdateStatusResult struct {
	Source    string   `json:"source"`
	URL       string   `json:"url"`
	Branch    string   `json:"branch"`
	Status    string   `json:"status"`
	LocalSHA  string   `json:"local_sha"`
	RemoteSHA string   `json:"remote_sha"`
	Skills    []string `json:"skills"`
	CachePath string   `json:"cache_path"`
	Error     string   `json:"error,omitempty"`
}

func sortedSkillKeys(skills map[string]string) []string {
	keys := make([]string, 0, len(skills))
	for key := range skills {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func CheckRepoUpdateStatus(source string, repo config.RemoteRepo, cacheDir string) UpdateStatusResult {
	return newRemoteSource(nil, source, repo, cacheDir).CheckStatus()
}

var checkRepoUpdateStatus = CheckRepoUpdateStatus

func CheckAllRemoteSkillsOutdated(cfg *config.Config, cacheDir string, workers int) []UpdateStatusResult {
	return checkRemoteSkillsOutdated(cfg.Remote, cacheDir, workers)
}

func checkRemoteSkillsOutdated(repositories map[string]config.RemoteRepo, cacheDir string, workers int) []UpdateStatusResult {
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
	results := make([]UpdateStatusResult, len(tasks))
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, current := range tasks {
		wg.Add(1)
		go func(index int, current task) {
			defer wg.Done()
			sem <- struct{}{}
			results[index] = checkRepoUpdateStatus(current.source, current.repo, cacheDir)
			<-sem
		}(i, current)
	}
	wg.Wait()
	sort.Slice(results, func(i, j int) bool { return strings.ToLower(results[i].Source) < strings.ToLower(results[j].Source) })
	return results
}

type UpdatedRepoInfo struct {
	Source string `json:"source"`
	NewSHA string `json:"new_sha,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}
type SkippedRepoInfo struct {
	Source   string `json:"source"`
	Reason   string `json:"reason"`
	LocalSHA string `json:"local_sha,omitempty"`
}
type UpdateErrorInfo struct {
	Source string `json:"source"`
	Error  string `json:"error"`
}
type UpdateResult struct {
	UpdatedRepos []UpdatedRepoInfo `json:"updated_repos"`
	SkippedRepos []SkippedRepoInfo `json:"skipped_repos"`
	Errors       []UpdateErrorInfo `json:"errors"`
}

const (
	UpdateCheckStart   = "check_start"
	UpdateCheckDone    = "check_done"
	UpdateRefreshStart = "refresh_start"
	UpdateRefreshDone  = "refresh_done"
	UpdateStart        = "update_start"
	UpdateRepoDone     = "repo_done"
	UpdateRepoError    = "repo_error"
)

type UpdateEvent struct {
	Kind, Source, NewSHA, Err        string
	Skills                           []string
	Index, Total, Outdated, UpToDate int
	DryRun                           bool
}
type UpdateProgress func(UpdateEvent)

func emitUpdate(progress UpdateProgress, event UpdateEvent) {
	if progress != nil {
		progress(event)
	}
}

func resolveUpdateSources(cfg *config.Config, targets []string) (map[string]config.RemoteRepo, error) {
	if len(targets) == 0 {
		return cfg.Remote, nil
	}
	selected := make(map[string]config.RemoteRepo)
	for _, raw := range targets {
		target := strings.ToLower(strings.TrimSpace(raw))
		matches := make(map[string]struct{})
		for source, repo := range cfg.Remote {
			if strings.EqualFold(source, target) {
				matches[source] = struct{}{}
			}
			parts := strings.Split(strings.ToLower(source), "/")
			matched := parts[len(parts)-1] == target
			for skill := range repo.Skills {
				if strings.EqualFold(skill, target) {
					matched = true
				}
			}
			if matched {
				matches[source] = struct{}{}
			}
		}
		matchedSources := make([]string, 0, len(matches))
		for source := range matches {
			matchedSources = append(matchedSources, source)
		}
		sort.Strings(matchedSources)
		if len(matchedSources) == 0 {
			return nil, fmt.Errorf("unknown update target %q", raw)
		}
		if len(matchedSources) > 1 {
			return nil, fmt.Errorf("ambiguous update target %q matches Sources: %s", raw, strings.Join(matchedSources, ", "))
		}
		selected[matchedSources[0]] = cfg.Remote[matchedSources[0]]
	}
	return selected, nil
}

// UpdateRemoteSkills keeps its historical signature for callers; skillsDir is
// intentionally ignored because Update now mutates only shared Cache data.
func UpdateRemoteSkills(cfg *config.Config, targets []string, force, dryRun bool, _ string, cacheDir string, progress UpdateProgress) (*UpdateResult, error) {
	repositories, err := resolveUpdateSources(cfg, targets)
	if err != nil {
		return nil, err
	}
	result := &UpdateResult{UpdatedRepos: []UpdatedRepoInfo{}, SkippedRepos: []SkippedRepoInfo{}, Errors: []UpdateErrorInfo{}}
	emitUpdate(progress, UpdateEvent{Kind: UpdateCheckStart, Total: len(repositories)})
	statuses := checkRemoteSkillsOutdated(repositories, cacheDir, 8)
	var refresh []string
	for _, status := range statuses {
		source := status.Source
		if !force && status.Status == "up_to_date" {
			result.SkippedRepos = append(result.SkippedRepos, SkippedRepoInfo{Source: source, Reason: "up_to_date", LocalSHA: status.LocalSHA})
			continue
		}
		if !force && status.Status == "error" {
			message := status.Error
			if message == "" {
				message = "failed to query remote repository"
			}
			result.Errors = append(result.Errors, UpdateErrorInfo{Source: source, Error: message})
			emitUpdate(progress, UpdateEvent{Kind: UpdateRepoError, Source: source, Err: message})
			continue
		}
		refresh = append(refresh, source)
	}
	emitUpdate(progress, UpdateEvent{Kind: UpdateCheckDone, Total: len(statuses), UpToDate: len(result.SkippedRepos), Outdated: len(refresh)})
	if !dryRun && len(refresh) > 0 {
		emitUpdate(progress, UpdateEvent{Kind: UpdateRefreshStart, Total: len(refresh)})
	}
	for i, source := range refresh {
		emitUpdate(progress, UpdateEvent{Kind: UpdateStart, Source: source, Index: i + 1, Total: len(refresh), DryRun: dryRun})
		if dryRun {
			result.UpdatedRepos = append(result.UpdatedRepos, UpdatedRepoInfo{Source: source, DryRun: true})
			continue
		}
		dir, refreshErr := newRemoteSource(nil, source, repositories[source], cacheDir).refresh(true)
		if refreshErr != nil {
			message := refreshErr.Error()
			result.Errors = append(result.Errors, UpdateErrorInfo{Source: source, Error: message})
			emitUpdate(progress, UpdateEvent{Kind: UpdateRepoError, Source: source, Err: message})
			continue
		}
		sha := GetLocalRepoCommit(dir)
		result.UpdatedRepos = append(result.UpdatedRepos, UpdatedRepoInfo{Source: source, NewSHA: sha})
		emitUpdate(progress, UpdateEvent{Kind: UpdateRepoDone, Source: source, NewSHA: sha})
	}
	if !dryRun && len(refresh) > 0 {
		emitUpdate(progress, UpdateEvent{Kind: UpdateRefreshDone, Total: len(refresh)})
	}
	return result, nil
}
