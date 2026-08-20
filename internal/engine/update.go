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
	cleanSource := strings.TrimSuffix(source, ".git")
	parsed := models.ParseRepoSource(source)

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
	remoteSHA := GetRemoteRepoCommit(source, repoURL, targetBranch)

	status := "up_to_date"
	if localSHA == "" {
		status = "not_cached"
	} else if remoteSHA == "" {
		status = "error"
	} else if localSHA != remoteSHA {
		status = "update_available"
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
	PostHooks     []HookResult      `json:"post_hooks"`
}

type ProgressCallback func(event string, data map[string]interface{})

func UpdateRemoteSkills(
	cfg *config.Config,
	targets []string,
	force bool,
	dryRun bool,
	skillsDir string,
	cacheDir string,
	runPostHooks bool,
	onProgress ProgressCallback,
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
		PostHooks:     make([]HookResult, 0),
	}

	reposNeedingUpdate := make(map[string]config.RemoteRepo)

	// 1. Fast parallel check if no targets specified and not forced
	if !force && targetSet == nil && len(reposToProcess) > 0 {
		if onProgress != nil {
			onProgress("check_start", map[string]interface{}{"total": len(reposToProcess)})
		}

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

					if !dryRun {
						for sk := range repoInfo.Skills {
							for _, agent := range GetTargetAgentsForSkill(sk, source, cfg, baseSkills) {
								_, _ = EnsureAgentSymlink(sk, agent, baseSkills)
							}
						}
					}
					continue
				}
			}
			reposNeedingUpdate[source] = repoInfo
		}

		if onProgress != nil {
			onProgress("check_done", map[string]interface{}{
				"total":      len(reposToProcess),
				"up_to_date": len(result.SkippedRepos),
				"outdated":   len(reposNeedingUpdate),
			})
		}
	} else {
		reposNeedingUpdate = reposToProcess
	}

	// 2. Process updates for repos that need them
	sources := make([]string, 0, len(reposNeedingUpdate))
	for src := range reposNeedingUpdate {
		sources = append(sources, src)
	}
	sort.Strings(sources)

	type repoResult struct {
		updated UpdatedRepoInfo
		skills  []string
		errors  []UpdateErrorInfo
	}
	results := make([]repoResult, len(sources))
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
				skillList := sortedSkillKeys(repoInfo.Skills)
				if onProgress != nil {
					onProgress("update_start", map[string]interface{}{"source": source, "index": i + 1, "total": len(sources), "skills": skillList, "dry_run": dryRun})
				}
				if dryRun {
					results[i] = repoResult{updated: UpdatedRepoInfo{Source: source, Skills: skillList, DryRun: true}, skills: skillList}
					continue
				}

				repoDir, err := EnsureGitRepo(source, repoInfo.URL, repoInfo.Branch, true, baseCache)
				if err != nil {
					errMsg := err.Error()
					results[i].errors = append(results[i].errors, UpdateErrorInfo{Source: source, Error: errMsg})
					if onProgress != nil {
						onProgress("repo_error", map[string]interface{}{"source": source, "error": errMsg})
					}
					continue
				}

				repoUpdatedSkills := make([]string, 0)
				for name, subpath := range repoInfo.Skills {
					srcPath := filepath.Join(repoDir, filepath.FromSlash(subpath))
					targetPath := filepath.Join(baseSkills, name)
					if _, err := os.Stat(srcPath); err != nil {
						errMsg := fmt.Sprintf("Path missing in repository: %s", subpath)
						results[i].errors = append(results[i].errors, UpdateErrorInfo{Source: source, Skill: name, Error: errMsg})
						if onProgress != nil {
							onProgress("repo_error", map[string]interface{}{"source": source, "skill": name, "error": errMsg})
						}
						continue
					}
					if err := CopySkillFolder(srcPath, targetPath); err != nil {
						results[i].errors = append(results[i].errors, UpdateErrorInfo{Source: source, Skill: name, Error: fmt.Sprintf("Failed to copy skill: %s", err)})
						continue
					}
					repoUpdatedSkills = append(repoUpdatedSkills, name)
					for _, agent := range GetTargetAgentsForSkill(name, source, cfg, baseSkills) {
						_, _ = EnsureAgentSymlink(name, agent, baseSkills)
					}
					if onProgress != nil {
						onProgress("skill_restored", map[string]interface{}{"source": source, "skill": name, "subpath": subpath})
					}
				}

				newSHA := GetLocalRepoCommit(repoDir)
				results[i].updated = UpdatedRepoInfo{Source: source, Skills: repoUpdatedSkills, NewSHA: newSHA}
				results[i].skills = repoUpdatedSkills
				if onProgress != nil {
					onProgress("repo_done", map[string]interface{}{"source": source, "new_sha": newSHA, "skills": repoUpdatedSkills})
				}
			}
		}()
	}
	for i := range sources {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	for _, repo := range results {
		result.UpdatedRepos = append(result.UpdatedRepos, repo.updated)
		result.UpdatedSkills = append(result.UpdatedSkills, repo.skills...)
		result.Errors = append(result.Errors, repo.errors...)
	}

	// 3. Post hooks
	if runPostHooks && len(result.UpdatedSkills) > 0 && !dryRun {
		if len(cfg.PostHooks) > 0 {
			if onProgress != nil {
				onProgress("hooks_start", map[string]interface{}{})
			}
			result.PostHooks = ExecutePostHooks(cfg.PostHooks, false)
			if onProgress != nil {
				for _, h := range result.PostHooks {
					onProgress("hook_done", map[string]interface{}{
						"name": h.Name,
						"ok":   h.Success,
						"msg":  h.Message,
					})
				}
			}
		}
	}

	return result, nil
}
