package engine

import (
	"fmt"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// remoteSource owns one remote Source's Cache and Freshness observation.
// Materialize and Availability are the shared apply path.
type remoteSource struct {
	key      string
	repo     config.RemoteRepo
	cacheDir string
}

func newRemoteSource(key string, repo config.RemoteRepo, cacheDir string) remoteSource {
	return remoteSource{key: key, repo: repo, cacheDir: cacheDir}
}

// PrepareRemoteSource refreshes one Source's Cache and discovers its Skills.
// Add uses this before it knows which Skills the user will declare.
func PrepareRemoteSource(key string, repo config.RemoteRepo, cacheDir, scope string) (string, DiscoveredSkills, error) {
	remote := newRemoteSource(key, repo, cacheDir)
	repoDir, err := remote.refresh(true)
	if err != nil {
		return "", nil, fmt.Errorf("refresh Source %s: %w", key, err)
	}
	discovered, err := DiscoverSkillsInRepo(repoDir, scope)
	if err != nil {
		return "", nil, fmt.Errorf("discover Skills in %s: %w", key, err)
	}
	return repoDir, discovered, nil
}

func (s remoteSource) refresh(force bool) (string, error) {
	return EnsureGitRepo(s.key, s.repo.URL, s.repo.Branch, force, s.cacheDir)
}

// ObserveFreshness queries local and remote git commit SHAs.
func (s remoteSource) ObserveFreshness() FreshnessRepository {
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

	localSHA := GetLocalRepoCommit(repo.Dir)
	remoteSHA := ""

	status := RemoteUpToDate
	if errorMessage != "" {
		status = RemoteError
	}
	if localSHA == "" {
		if status != RemoteError {
			status = RemoteNotCached
		}
	} else if status != RemoteError {
		remoteSHA = defaultRemoteSHA
		if remoteSHA == "" {
			var remoteErr error
			remoteSHA, remoteErr = GetRemoteRepoCommitResult(s.key, repo.URL, targetBranch)
			if remoteErr != nil {
				status = RemoteError
				errorMessage = remoteErr.Error()
			}
		}
		if status != RemoteError && localSHA != remoteSHA {
			status = RemoteUpdateAvailable
		}
	}

	return FreshnessRepository{
		Source:       s.key,
		URL:          repo.URL,
		Branch:       targetBranch,
		RemoteStatus: status,
		LocalSHA:     localSHA,
		RemoteSHA:    remoteSHA,
		CachePath:    repo.Dir,
		Error:        errorMessage,
	}
}
