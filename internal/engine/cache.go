package engine

import (
	"fmt"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// Cache is one remote Source's working copy. It stores Source identity, not
// Config: callers pass the Skill subpaths each operation should cover.
type Cache struct {
	key      string
	url      string
	branch   string
	cacheDir string
}

func NewCache(key, url, branch, cacheDir string) Cache {
	return Cache{key: key, url: url, branch: branch, cacheDir: cacheDir}
}

func (c Cache) repo() cacheRepo {
	return resolveCacheRepo(c.key, c.url, c.branch, c.cacheDir)
}

func (c Cache) dir() string {
	return c.repo().Dir
}

// Refresh clones or fetches this Cache and covers paths in its sparse checkout.
func (c Cache) Refresh(force bool, paths ...string) (string, error) {
	return ensureGitRepo(c.key, c.url, c.branch, force, c.cacheDir, paths...)
}

// Cover adds paths to this Cache's sparse checkout without fetching a new commit.
func (c Cache) Cover(paths ...string) error {
	return ensureSparsePaths(c.dir(), paths)
}

type cacheFacts struct {
	source    string
	url       string
	branch    string
	dir       string
	localSHA  string
	remoteSHA string
	missing   []string
	err       string
}

// observe reports git facts for this Cache. Classification into RemoteStatus
// is Freshness, not Cache.
func (c Cache) observe(paths ...string) cacheFacts {
	repo := c.repo()
	facts := cacheFacts{source: c.key, url: repo.URL, branch: repo.Branch, dir: repo.Dir}
	defaultRemoteSHA := ""
	if c.branch == "" && models.ParseRepoSource(c.key).Branch == "" {
		resolvedBranch, resolvedSHA, err := getRemoteDefaultBranchCommit(c.key, repo.URL)
		if err != nil {
			facts.err = err.Error()
		} else {
			repo = resolveCacheRepo(c.key, c.url, resolvedBranch, c.cacheDir)
			facts.url = repo.URL
			facts.branch = repo.Branch
			facts.dir = repo.Dir
			defaultRemoteSHA = resolvedSHA
		}
	}
	displayBranch := facts.branch
	if displayBranch == "" {
		displayBranch = "HEAD"
	}
	facts.branch = displayBranch
	facts.localSHA = localRepoCommit(facts.dir)
	if facts.err != "" || facts.localSHA == "" {
		return facts
	}
	facts.remoteSHA = defaultRemoteSHA
	if facts.remoteSHA == "" {
		remoteSHA, remoteErr := remoteRepoCommit(c.key, c.url, repo.Branch)
		if remoteErr != nil {
			facts.err = remoteErr.Error()
			return facts
		}
		facts.remoteSHA = remoteSHA
	}
	if facts.localSHA != facts.remoteSHA {
		return facts
	}
	missing, err := missingSparsePaths(facts.dir, paths)
	if err != nil {
		facts.err = err.Error()
		return facts
	}
	facts.missing = missing
	return facts
}

func (c Cache) withSkillFiles(fn func(repoDir string) error) error {
	repoDir := c.dir()
	return withSkillFiles(repoDir, func() error {
		return fn(repoDir)
	})
}

// PrepareRemoteSource refreshes one Source's Cache and discovers its Skills.
// Add uses this before it knows which Skills the user will declare.
func PrepareRemoteSource(key string, repo config.RemoteRepo, cacheDir, scope string) (string, DiscoveredSkills, error) {
	cache := NewCache(key, repo.URL, repo.Branch, cacheDir)
	repoDir, err := cache.Refresh(true, declaredSubpaths(repo)...)
	if err != nil {
		return "", nil, fmt.Errorf("refresh Source %s: %w", key, err)
	}
	discovered, err := discoverRemoteSkills(cache, scope)
	if err != nil {
		return "", nil, fmt.Errorf("discover Skills in %s: %w", key, err)
	}
	return repoDir, discovered, nil
}

func declaredSubpaths(repo config.RemoteRepo) []string {
	paths := make([]string, 0, len(repo.Skills))
	for _, name := range sortedSkillKeys(repo.Skills) {
		paths = append(paths, repo.Skills[name])
	}
	return paths
}
