package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// remoteSource owns one remote Source's Cache, Materialize, and Availability
// lifecycle. Batch scheduling remains with Sync and Update.
type remoteSource struct {
	availability *Availability
	key          string
	repo         config.RemoteRepo
	cacheDir     string
}

func newRemoteSource(availability *Availability, key string, repo config.RemoteRepo, cacheDir string) remoteSource {
	return remoteSource{availability: availability, key: key, repo: repo, cacheDir: cacheDir}
}

// PrepareRemoteSource refreshes one Source's Cache and discovers its Skills.
// Add uses this before it knows which Skills the user will declare.
func PrepareRemoteSource(key string, repo config.RemoteRepo, cacheDir string) (string, map[string]string, error) {
	remote := newRemoteSource(nil, key, repo, cacheDir)
	repoDir, err := remote.refresh(true)
	if err != nil {
		return "", nil, fmt.Errorf("refresh Source %s: %w", key, err)
	}
	discovered, err := DiscoverSkillsInRepo(repoDir)
	if err != nil {
		return "", nil, fmt.Errorf("discover Skills in %s: %w", key, err)
	}
	return repoDir, discovered, nil
}

func (s remoteSource) missingSkills(force bool) map[string]string {
	missing := make(map[string]string)
	for name, subpath := range s.repo.Skills {
		if force {
			missing[name] = subpath
			continue
		}
		if _, err := os.Stat(filepath.Join(s.availability.skillsDir, name)); err != nil {
			missing[name] = subpath
		}
	}
	return missing
}

func (s remoteSource) refresh(force bool) (string, error) {
	return EnsureGitRepo(s.key, s.repo.URL, s.repo.Branch, force, s.cacheDir)
}

// CheckStatus queries local and remote git commit SHAs for freshness.
func (s remoteSource) CheckStatus() UpdateStatusResult {
	repo := resolveCacheRepo(s.key, s.repo.URL, s.repo.Branch, s.cacheDir)
	errorMessage := ""
	defaultRemoteSHA := ""
	if s.repo.Branch == "" && models.ParseRepoSource(s.key).Branch == "" {
		resolvedBranch, resolvedSHA, err := getRemoteDefaultBranchCommit(s.key, repo.URL)
		if err != nil {
			errorMessage = err.Error()
		} else {
			repo = resolveCacheRepo(s.key, s.repo.URL, resolvedBranch, s.cacheDir)
			defaultRemoteSHA = resolvedSHA
		}
	}
	targetBranch := repo.Branch
	if targetBranch == "" {
		targetBranch = "HEAD"
	}

	skillList := sortedSkillKeys(s.repo.Skills)
	localSHA := GetLocalRepoCommit(repo.Dir)
	remoteSHA := ""

	status := "up_to_date"
	if errorMessage != "" {
		status = "error"
	}
	if localSHA == "" {
		if status != "error" {
			status = "not_cached"
		}
	} else if status != "error" {
		remoteSHA = defaultRemoteSHA
		if remoteSHA == "" {
			var remoteErr error
			remoteSHA, remoteErr = GetRemoteRepoCommitResult(s.key, repo.URL, targetBranch)
			if remoteErr != nil {
				status = "error"
				errorMessage = remoteErr.Error()
			}
		}
		if status != "error" && localSHA != remoteSHA {
			status = "update_available"
		}
	}

	return UpdateStatusResult{
		Source:    s.key,
		URL:       repo.URL,
		Branch:    targetBranch,
		Status:    status,
		LocalSHA:  localSHA,
		RemoteSHA: remoteSHA,
		Skills:    skillList,
		CachePath: repo.Dir,
		Error:     errorMessage,
	}
}

// reconcile Materializes selected Skills, then applies Availability for every
// Skill in the Source. Materialize failures are events and continue;
// Availability failures fail closed. dryRun never writes.
func (s remoteSource) reconcile(repoDir string, dryRun bool, toWrite map[string]string, emit func(SyncEvent)) error {
	if emit == nil {
		emit = func(SyncEvent) {}
	}
	for _, name := range sortedSkillKeys(s.repo.Skills) {
		subpath := s.repo.Skills[name]
		if !dryRun && toWrite != nil {
			if _, write := toWrite[name]; write {
				if err := MaterializeRemoteSkill(name, subpath, repoDir, s.availability.skillsDir); err != nil {
					if errors.Is(err, errRepoPathMissing) {
						emit(SyncEvent{Kind: SyncPathMissing, Source: s.key, Skill: name, Path: subpath})
					} else {
						emit(SyncEvent{Kind: SyncCopyFailed, Source: s.key, Skill: name, Err: err.Error()})
					}
					continue
				}
				emit(SyncEvent{Kind: SyncMaterialized, Source: s.key, Skill: name, Path: subpath})
			}
		}
		if err := s.availability.applyDeclared(name, s.key, dryRun, emit); err != nil {
			return err
		}
	}
	return nil
}

// sync reconciles one declared remote Source end to end.
func (s remoteSource) sync(force, dryRun bool, emit func(SyncEvent)) error {
	missing := s.missingSkills(force)
	if len(missing) == 0 && !force {
		return s.reconcile("", dryRun, nil, emit)
	}

	emit(SyncEvent{Kind: SyncRepoStart, Source: s.key, Skills: sortedSkillKeys(s.repo.Skills)})
	repoDir := resolveCacheRepo(s.key, s.repo.URL, s.repo.Branch, s.cacheDir).Dir
	if GetLocalRepoCommit(repoDir) == "" {
		emit(SyncEvent{Kind: SyncFetchFailed, Source: s.key, Err: fmt.Sprintf("Cache missing for Source %s; run 'skills update' first", s.key)})
		return nil
	}
	if dryRun {
		emit(SyncEvent{Kind: SyncWouldSync, Source: s.key, Skills: sortedSkillKeys(missing)})
		return s.reconcile("", true, nil, emit)
	}
	return s.reconcile(repoDir, false, missing, emit)
}
