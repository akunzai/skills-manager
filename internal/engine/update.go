package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

type UpdateStatusResult struct {
	Source    string   `json:"source"`
	URL       string   `json:"url"`
	Branch    string   `json:"branch"`
	Status    string   `json:"status"` // "up_to_date", "update_available", "not_installed", "error"
	LocalSHA  string   `json:"localSha"`
	RemoteSHA string   `json:"remoteSha"`
	Skills    []string `json:"skills"`
	CachePath string   `json:"cachePath,omitempty"`
}

func sortedSkillKeys(skills map[string]string) []string {
	keys := make([]string, 0, len(skills))
	for k := range skills {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func CheckRepoUpdateStatus(source string, repoInfo config.RemoteRepo, cacheDir string) UpdateStatusResult {
	parsed := models.ParseRepoSource(source)
	cleanSource := parsed.SourceKey

	baseCache := cacheDir
	if baseCache == "" {
		baseCache = models.DefaultCacheDir()
	}
	repoDest := filepath.Join(baseCache, filepath.FromSlash(cleanSource))

	repoURL := repoInfo.URL
	if repoURL == "" {
		repoURL = parsed.URL
	}
	targetBranch := repoInfo.Branch
	if targetBranch == "" {
		targetBranch = parsed.Branch
	}
	if targetBranch == "" {
		targetBranch = "HEAD"
	}

	skillList := sortedSkillKeys(repoInfo.Skills)

	localSHA := GetLocalRepoCommit(repoDest)
	remoteSHA := ""

	status := "up_to_date"
	if localSHA == "" {
		status = "not_cached"
	} else {
		remoteSHA = GetRemoteRepoCommit(source, repoURL, targetBranch)
		if remoteSHA == "" {
			status = "error"
		} else if localSHA != remoteSHA {
			status = "update_available"
		}
	}

	return UpdateStatusResult{
		Source:    source,
		URL:       repoURL,
		Branch:    targetBranch,
		Status:    status,
		LocalSHA:  localSHA,
		RemoteSHA: remoteSHA,
		Skills:    skillList,
		CachePath: repoDest,
	}
}

func CheckAllRemoteSkillsOutdated(cfg *config.Config, cacheDir string, maxWorkers int) []UpdateStatusResult {
	if len(cfg.Remote) == 0 {
		return []UpdateStatusResult{}
	}

	if maxWorkers <= 0 {
		maxWorkers = 8
	}

	type task struct {
		source   string
		repoInfo config.RemoteRepo
	}

	tasks := make([]task, 0, len(cfg.Remote))
	for src, rInfo := range cfg.Remote {
		tasks = append(tasks, task{source: src, repoInfo: rInfo})
	}

	results := make([]UpdateStatusResult, len(tasks))
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxWorkers)

	for i, t := range tasks {
		wg.Add(1)
		go func(idx int, tsk task) {
			defer wg.Done()
			sem <- struct{}{}
			results[idx] = CheckRepoUpdateStatus(tsk.source, tsk.repoInfo, cacheDir)
			<-sem
		}(i, t)
	}

	wg.Wait()

	sort.Slice(results, func(i, j int) bool {
		return strings.ToLower(results[i].Source) < strings.ToLower(results[j].Source)
	})

	return results
}

type UpdatedRepoInfo struct {
	Source string   `json:"source"`
	Skills []string `json:"skills"`
	NewSHA string   `json:"new_sha,omitempty"`
	DryRun bool     `json:"dry_run,omitempty"`
}

type SkippedRepoInfo struct {
	Source   string   `json:"source"`
	Reason   string   `json:"reason"`
	LocalSHA string   `json:"local_sha,omitempty"`
	Skills   []string `json:"skills"`
}

type UpdateErrorInfo struct {
	Source string `json:"source"`
	Skill  string `json:"skill,omitempty"`
	Error  string `json:"error"`
}

type UpdateResult struct {
	UpdatedRepos  []UpdatedRepoInfo `json:"updated_repos"`
	UpdatedSkills []string          `json:"updated_skills"`
	SkippedRepos  []SkippedRepoInfo `json:"skipped_repos"`
	Errors        []UpdateErrorInfo `json:"errors"`
}

const (
	UpdateCheckStart    = "check_start"
	UpdateCheckDone     = "check_done"
	UpdateStart         = "update_start"
	UpdateSkillRestored = "skill_restored"
	UpdateRepoDone      = "repo_done"
	UpdateRepoError     = "repo_error"
	UpdateWouldDrift    = "would_drift"
)

// UpdateEvent is one step of UpdateRemoteSkills for the CLI to print.
type UpdateEvent struct {
	Kind       string
	Source     string
	Skill      string
	Skills     []string
	Index      int
	Total      int
	Outdated   int
	UpToDate   int
	DryRun     bool
	NewSHA     string
	Subpath    string
	Err        string
	Missing    []string
	Unexpected []string
}

type UpdateProgress func(UpdateEvent)

func emitUpdate(onProgress UpdateProgress, ev UpdateEvent) {
	if onProgress != nil {
		onProgress(ev)
	}
}

func updateReconcileEmit(onProgress UpdateProgress, result *UpdateResult, source string, restored *[]string) func(SyncEvent) {
	return func(ev SyncEvent) {
		switch ev.Kind {
		case SyncMaterialized:
			if restored != nil {
				*restored = append(*restored, ev.Skill)
			}
			emitUpdate(onProgress, UpdateEvent{Kind: UpdateSkillRestored, Source: source, Skill: ev.Skill, Subpath: ev.Path})
		case SyncPathMissing:
			errMsg := fmt.Sprintf("Path missing in repository: %s", ev.Path)
			result.Errors = append(result.Errors, UpdateErrorInfo{Source: source, Skill: ev.Skill, Error: errMsg})
			emitUpdate(onProgress, UpdateEvent{Kind: UpdateRepoError, Source: source, Skill: ev.Skill, Err: errMsg})
		case SyncCopyFailed:
			errMsg := fmt.Sprintf("Failed to copy skill: %s", ev.Err)
			result.Errors = append(result.Errors, UpdateErrorInfo{Source: source, Skill: ev.Skill, Error: errMsg})
			emitUpdate(onProgress, UpdateEvent{Kind: UpdateRepoError, Source: source, Skill: ev.Skill, Err: errMsg})
		case SyncWouldDrift:
			emitUpdate(onProgress, UpdateEvent{Kind: UpdateWouldDrift, Skill: ev.Skill, Missing: ev.Missing, Unexpected: ev.Unexpected})
		}
	}
}

func UpdateRemoteSkills(
	cfg *config.Config,
	targets []string,
	force bool,
	dryRun bool,
	skillsDir string,
	cacheDir string,
	onProgress UpdateProgress,
) (*UpdateResult, error) {
	baseSkills := skillsDir
	if baseSkills == "" {
		baseSkills = models.DefaultSkillsDir()
	}
	baseCache := cacheDir
	if baseCache == "" {
		baseCache = models.DefaultCacheDir()
	}

	if err := os.MkdirAll(baseSkills, 0755); err != nil {
		return nil, fmt.Errorf("failed to create skills dir: %w", err)
	}

	var targetSet map[string]struct{}
	if len(targets) > 0 {
		targetSet = make(map[string]struct{})
		for _, t := range targets {
			targetSet[strings.ToLower(strings.TrimSpace(t))] = struct{}{}
		}
	}

	reposToProcess := make(map[string]config.RemoteRepo)
	for source, repoInfo := range cfg.Remote {
		if targetSet == nil {
			reposToProcess[source] = repoInfo
		} else {
			sourceLower := strings.ToLower(source)
			parts := strings.Split(sourceLower, "/")
			repoName := parts[len(parts)-1]

			matched := false
			if _, ok := targetSet[sourceLower]; ok {
				matched = true
			} else if _, ok := targetSet[repoName]; ok {
				matched = true
			} else {
				for sk := range repoInfo.Skills {
					if _, ok := targetSet[strings.ToLower(sk)]; ok {
						matched = true
						break
					}
				}
			}

			if matched {
				reposToProcess[source] = repoInfo
			}
		}
	}

	result := &UpdateResult{
		UpdatedRepos:  make([]UpdatedRepoInfo, 0),
		UpdatedSkills: make([]string, 0),
		SkippedRepos:  make([]SkippedRepoInfo, 0),
		Errors:        make([]UpdateErrorInfo, 0),
	}

	reposNeedingUpdate := make(map[string]config.RemoteRepo)

	// 1. Fast parallel check if no targets specified and not forced
	if !force && targetSet == nil && len(reposToProcess) > 0 {
		emitUpdate(onProgress, UpdateEvent{Kind: UpdateCheckStart, Total: len(reposToProcess)})

		statusResults := CheckAllRemoteSkillsOutdated(cfg, baseCache, 8)
		statusMap := make(map[string]UpdateStatusResult)
		for _, r := range statusResults {
			statusMap[r.Source] = r
		}

		for source, repoInfo := range reposToProcess {
			statusInfo := statusMap[source]
			if statusInfo.Status == "up_to_date" {
				allExist := true
				for sk := range repoInfo.Skills {
					skPath := filepath.Join(baseSkills, sk)
					if _, err := os.Stat(skPath); err != nil {
						allExist = false
						break
					}
				}

				if allExist {
					skillList := sortedSkillKeys(repoInfo.Skills)

					result.SkippedRepos = append(result.SkippedRepos, SkippedRepoInfo{
						Source:   source,
						Reason:   "up_to_date",
						LocalSHA: statusInfo.LocalSHA,
						Skills:   skillList,
					})

					if err := reconcileRemoteSource(cfg, source, repoInfo.Skills, "", baseSkills, dryRun, nil, updateReconcileEmit(onProgress, result, source, nil)); err != nil {
						return result, err
					}
					continue
				}
			}
			reposNeedingUpdate[source] = repoInfo
		}

		emitUpdate(onProgress, UpdateEvent{
			Kind:     UpdateCheckDone,
			Total:    len(reposToProcess),
			UpToDate: len(result.SkippedRepos),
			Outdated: len(reposNeedingUpdate),
		})
	} else {
		reposNeedingUpdate = reposToProcess
	}

	// 2. Process updates for repos that need them. Cache refresh may run in
	// parallel; Availability apply stays sequential.
	sources := make([]string, 0, len(reposNeedingUpdate))
	for src := range reposNeedingUpdate {
		sources = append(sources, src)
	}
	sort.Strings(sources)

	type fetchedRepo struct {
		dir string
		err error
	}
	fetched := make([]fetchedRepo, len(sources))
	if !dryRun && len(sources) > 0 {
		jobs := make(chan int)
		var wg sync.WaitGroup
		workers := 8
		if workers > len(sources) {
			workers = len(sources)
		}
		for worker := 0; worker < workers; worker++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range jobs {
					source := sources[i]
					repoInfo := reposNeedingUpdate[source]
					dir, err := EnsureGitRepo(source, repoInfo.URL, repoInfo.Branch, true, baseCache)
					fetched[i] = fetchedRepo{dir: dir, err: err}
				}
			}()
		}
		for i := range sources {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
	}

	for i, source := range sources {
		repoInfo := reposNeedingUpdate[source]
		skillList := sortedSkillKeys(repoInfo.Skills)
		emitUpdate(onProgress, UpdateEvent{Kind: UpdateStart, Source: source, Index: i + 1, Total: len(sources), Skills: skillList, DryRun: dryRun})
		if dryRun {
			result.UpdatedRepos = append(result.UpdatedRepos, UpdatedRepoInfo{Source: source, Skills: skillList, DryRun: true})
			result.UpdatedSkills = append(result.UpdatedSkills, skillList...)
			if err := reconcileRemoteSource(cfg, source, repoInfo.Skills, "", baseSkills, true, nil, updateReconcileEmit(onProgress, result, source, nil)); err != nil {
				return result, err
			}
			continue
		}

		if fetched[i].err != nil {
			errMsg := fetched[i].err.Error()
			result.Errors = append(result.Errors, UpdateErrorInfo{Source: source, Error: errMsg})
			emitUpdate(onProgress, UpdateEvent{Kind: UpdateRepoError, Source: source, Err: errMsg})
			continue
		}

		repoDir := fetched[i].dir
		repoUpdatedSkills := make([]string, 0)
		if err := reconcileRemoteSource(cfg, source, repoInfo.Skills, repoDir, baseSkills, false, repoInfo.Skills, updateReconcileEmit(onProgress, result, source, &repoUpdatedSkills)); err != nil {
			return result, err
		}

		newSHA := GetLocalRepoCommit(repoDir)
		result.UpdatedRepos = append(result.UpdatedRepos, UpdatedRepoInfo{Source: source, Skills: repoUpdatedSkills, NewSHA: newSHA})
		result.UpdatedSkills = append(result.UpdatedSkills, repoUpdatedSkills...)
		emitUpdate(onProgress, UpdateEvent{Kind: UpdateRepoDone, Source: source, NewSHA: newSHA, Skills: repoUpdatedSkills})
	}

	return result, nil
}
