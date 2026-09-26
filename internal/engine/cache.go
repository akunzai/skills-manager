package engine

import (
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"

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

// diffOptions pin git's patch format against the user's git config, so the
// patch parses the same way everywhere: plain text, a/ and b/ prefixes, no
// rename pairing and no external or text-converting drivers.
var diffOptions = []string{"diff", "--no-color", "--no-ext-diff", "--no-textconv", "--no-renames"}

// diffCommits is the patch of subpath from one commit of this Cache to
// another, with paths relative to subpath. A blob the partial clone never
// fetched is fetched on demand, which is the only way this reaches the network.
func (c Cache) diffCommits(from, to, subpath string) (string, error) {
	args := append(slices.Clone(diffOptions), "--src-prefix=a/", "--dst-prefix=b/")
	subpath = cleanSparsePaths([]string{subpath})[0]
	if subpath != "." {
		args = append(args, "--relative="+subpath)
	}
	args = append(args, from, to, "--")
	if subpath != "." {
		args = append(args, subpath)
	}
	stdout, stderr, err := runGitRaw(c.dir(), args...)
	if err != nil {
		return "", gitOpErr("diff", c.dir(), string(stdout), stderr, err)
	}
	return string(stdout), nil
}

// readBlob reads the file at path, relative to the repository root, as
// commit has it. A symlink reads as its target, as DigestSkillContent hashes it.
func (c Cache) readBlob(commit, path string) ([]byte, error) {
	stdout, stderr, err := runGitRaw(c.dir(), "cat-file", "blob", commit+":"+path)
	if err != nil {
		return nil, gitOpErr("read "+path+" at "+commit+" in", c.dir(), "", stderr, err)
	}
	return stdout, nil
}

// diffDirs is the patch from dir/a to dir/b, two trees of files outside any
// repository, with paths relative to each tree as a/<path> and b/<path>.
func diffDirs(dir string) (string, error) {
	args := append(slices.Clone(diffOptions), "--no-index", "--no-prefix", "a", "b")
	stdout, stderr, err := runGitRaw(dir, args...)
	// --no-index exits 1 when the trees differ.
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ExitCode() == 1 {
		err = nil
	}
	if err != nil {
		return "", gitOpErr("diff", dir, string(stdout), stderr, err)
	}
	// Without prefixes, the "diff --git" line of an added or removed file
	// names the one tree it is in on both sides: "b/new b/new". The ---
	// and +++ lines already read a/<path> and b/<path>.
	lines := strings.SplitAfter(string(stdout), "\n")
	for i, line := range lines {
		if rest, ok := strings.CutPrefix(line, "diff --git "); ok {
			if src, ok := diffGitPath(rest); ok {
				lines[i] = "diff --git " + setTreeName(src, 'a') + " " + setTreeName(src, 'b') + "\n"
			}
		}
	}
	return strings.Join(lines, ""), nil
}

// diffGitPath returns one side of a "diff --git" header's paths. Without
// renames both sides name the same path, differing only in their a/ or b/,
// so the header is two equal halves around one space.
func diffGitPath(rest string) (string, bool) {
	rest = strings.TrimSuffix(rest, "\n")
	if len(rest)%2 == 0 {
		return "", false
	}
	half := len(rest) / 2
	return rest[:half], rest[half] == ' '
}

// setTreeName replaces the a or b that starts a patch path, quoted or not.
func setTreeName(path string, tree byte) string {
	b := []byte(path)
	i := 0
	if len(b) > 0 && b[0] == '"' {
		i = 1
	}
	if len(b) > i+1 && (b[i] == 'a' || b[i] == 'b') && b[i+1] == '/' {
		b[i] = tree
	}
	return string(b)
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
	repoDir, discovered, _, err := prepareRemoteSource(key, repo, cacheDir, scope)
	return repoDir, discovered, err
}

// PrepareRemoteSourceWithDescriptions is PrepareRemoteSource plus each
// candidate's description, for Add's --list, which shows what Add would
// offer without a second fetch.
func PrepareRemoteSourceWithDescriptions(key string, repo config.RemoteRepo, cacheDir, scope string) (string, DiscoveredSkills, DiscoveredSkillDescriptions, error) {
	return prepareRemoteSource(key, repo, cacheDir, scope)
}

func prepareRemoteSource(key string, repo config.RemoteRepo, cacheDir, scope string) (string, DiscoveredSkills, DiscoveredSkillDescriptions, error) {
	cache := NewCache(key, repo.URL, repo.Branch, cacheDir)
	repoDir, err := cache.Refresh(true, declaredSubpaths(repo)...)
	if err != nil {
		return "", nil, nil, fmt.Errorf("refresh Source %s: %w", key, err)
	}
	discovered, descriptions, err := discoverRemoteSkills(cache, scope)
	if err != nil {
		return "", nil, nil, fmt.Errorf("discover Skills in %s: %w", key, err)
	}
	return repoDir, discovered, descriptions, nil
}

func declaredSubpaths(repo config.RemoteRepo) []string {
	paths := make([]string, 0, len(repo.Skills))
	for _, name := range sortedSkillKeys(repo.Skills) {
		paths = append(paths, repo.Skills[name])
	}
	return paths
}
