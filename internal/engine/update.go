package engine

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
)

func sortedSkillKeys(skills map[string]string) []string {
	return slices.Sorted(maps.Keys(skills))
}

type UpdatedRepoInfo struct {
	Source string `json:"source"`
	NewSHA string `json:"new_sha,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// SkippedSkillsUnchanged is a Source whose Cache moved to a new commit that
// left every declared Skill's tree as it was.
const SkippedSkillsUnchanged = "skills_unchanged"

type SkippedRepoInfo struct {
	Source   string `json:"source"`
	Reason   string `json:"reason"`
	LocalSHA string `json:"local_sha,omitempty"`
}
type UpdateErrorInfo struct {
	Source string `json:"source"`
	Error  string `json:"error"`
}

// RenamedSkillInfo is a declared Skill its Source removed while another Skill
// declared it replaces it. Update covers the replacement; Sync migrates.
type RenamedSkillInfo struct {
	Source  string `json:"source"`
	From    string `json:"from"`
	To      string `json:"to"`
	Subpath string `json:"subpath"`
}
type UpdateResult struct {
	UpdatedRepos []UpdatedRepoInfo  `json:"updated_repos"`
	SkippedRepos []SkippedRepoInfo  `json:"skipped_repos"`
	Renamed      []RenamedSkillInfo `json:"renamed"`
	Errors       []UpdateErrorInfo  `json:"errors"`
}

const (
	UpdateCheckStart    = "check_start"
	UpdateCheckDone     = "check_done"
	UpdateRefreshStart  = "refresh_start"
	UpdateRefreshDone   = "refresh_done"
	UpdateStart         = "update_start"
	UpdateRepoDone      = "repo_done"
	UpdateRepoUnchanged = "repo_unchanged"
	UpdateRepoError     = "repo_error"
	UpdateRenamed       = "renamed"
)

type UpdateEvent struct {
	Kind, Source, NewSHA, Err string
	From, To                  string
	Skills                    []string
	Index, Total              int
	DryRun                    bool
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
		matchedSources := slices.Sorted(maps.Keys(matches))
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

func UpdateRemoteSkills(cfg *config.Config, targets []string, force, dryRun bool, cacheDir string, progress UpdateProgress) (*UpdateResult, error) {
	repositories, err := resolveUpdateSources(cfg, targets)
	if err != nil {
		return nil, err
	}
	result := &UpdateResult{UpdatedRepos: []UpdatedRepoInfo{}, SkippedRepos: []SkippedRepoInfo{}, Renamed: []RenamedSkillInfo{}, Errors: []UpdateErrorInfo{}}
	emitUpdate(progress, UpdateEvent{Kind: UpdateCheckStart, Total: len(repositories)})
	selected := *config.DefaultConfig()
	selected.Remote = repositories
	snapshot, err := InspectFreshness(&selected, "", cacheDir, FreshnessOptions{ObserveRemote: true, Workers: 8})
	if err != nil {
		return nil, err
	}
	needsUpdate := make(map[string]struct{})
	for _, disposition := range snapshot.Dispositions() {
		if disposition.Kind == FreshnessUpdate {
			needsUpdate[disposition.Source] = struct{}{}
		}
	}
	var refresh []string
	incomplete := make(map[string]bool)
	for _, status := range snapshot.Repositories {
		incomplete[status.Source] = status.RemoteStatus == RemoteCacheIncomplete
		source := status.Source
		_, update := needsUpdate[source]
		if !force && !update {
			result.SkippedRepos = append(result.SkippedRepos, SkippedRepoInfo{Source: source, Reason: "up_to_date", LocalSHA: status.LocalSHA})
			continue
		}
		refresh = append(refresh, source)
	}
	emitUpdate(progress, UpdateEvent{Kind: UpdateCheckDone, Total: len(snapshot.Repositories)})
	if !dryRun && len(refresh) > 0 {
		emitUpdate(progress, UpdateEvent{Kind: UpdateRefreshStart, Total: len(refresh)})
	}
	for i, source := range refresh {
		emitUpdate(progress, UpdateEvent{Kind: UpdateStart, Source: source, Index: i + 1, Total: len(refresh), DryRun: dryRun})
		if dryRun {
			result.UpdatedRepos = append(result.UpdatedRepos, UpdatedRepoInfo{Source: source, DryRun: true})
			continue
		}
		// A Cache that only lacks a declared Skill is already at the remote
		// commit; adding the path is enough.
		fetch := force || !incomplete[source]
		cache := NewCache(source, repositories[source].URL, repositories[source].Branch, cacheDir)
		paths := declaredSubpaths(repositories[source])
		// Comparing tree IDs needs no file contents, so a commit elsewhere in
		// the Source is told apart from one that changed a declared Skill.
		declaredTrees := func() map[string]string {
			trees := make(map[string]string, len(paths))
			for _, p := range paths {
				trees[p] = cache.atHead(p).id
			}
			return trees
		}
		beforeDir := cache.dir()
		beforeSHA := cache.head()
		beforeTrees := declaredTrees()
		dir, refreshErr := cache.Refresh(fetch, paths...)
		if refreshErr != nil {
			message := refreshErr.Error()
			result.Errors = append(result.Errors, UpdateErrorInfo{Source: source, Error: message})
			emitUpdate(progress, UpdateEvent{Kind: UpdateRepoError, Source: source, Err: message})
			continue
		}
		sha := cache.head()
		if beforeSHA != "" && dir == beforeDir && sha != beforeSHA && maps.Equal(beforeTrees, declaredTrees()) {
			result.SkippedRepos = append(result.SkippedRepos, SkippedRepoInfo{Source: source, Reason: SkippedSkillsUnchanged, LocalSHA: sha})
			emitUpdate(progress, UpdateEvent{Kind: UpdateRepoUnchanged, Source: source, NewSHA: sha})
			continue
		}
		result.UpdatedRepos = append(result.UpdatedRepos, UpdatedRepoInfo{Source: source, NewSHA: sha})
		emitUpdate(progress, UpdateEvent{Kind: UpdateRepoDone, Source: source, NewSHA: sha})
	}
	if !dryRun && len(refresh) > 0 {
		emitUpdate(progress, UpdateEvent{Kind: UpdateRefreshDone, Total: len(refresh)})
	}
	if !dryRun {
		followRenames(repositories, cacheDir, result, progress)
	}
	return result, nil
}

// followRenames covers the replacement of every declared Skill its Source no
// longer has. It also runs for a Cache that was already current, since
// another Scope's update may have fetched the commit that removed the Skill.
func followRenames(repositories map[string]config.RemoteRepo, cacheDir string, result *UpdateResult, progress UpdateProgress) {
	failed := make(map[string]bool)
	for _, e := range result.Errors {
		failed[e.Source] = true
	}
	for _, source := range slices.Sorted(maps.Keys(repositories)) {
		repo := repositories[source]
		cache := NewCache(source, repo.URL, repo.Branch, cacheDir)
		if failed[source] || cache.head() == "" {
			continue
		}
		found, err := coverReplacements(cache, repo.Skills)
		if err != nil {
			message := fmt.Sprintf("follow renamed Skills: %v", err)
			result.Errors = append(result.Errors, UpdateErrorInfo{Source: source, Error: message})
			emitUpdate(progress, UpdateEvent{Kind: UpdateRepoError, Source: source, Err: message})
			continue
		}
		for _, old := range slices.Sorted(maps.Keys(found)) {
			replacement := found[old]
			result.Renamed = append(result.Renamed, RenamedSkillInfo{Source: source, From: old, To: replacement.Name, Subpath: replacement.Subpath})
			emitUpdate(progress, UpdateEvent{Kind: UpdateRenamed, Source: source, From: old, To: replacement.Name})
		}
	}
}
