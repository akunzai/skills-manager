package engine

import (
	"cmp"
	"errors"
	"os/exec"
	"slices"
	"strings"

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

// head is the commit this Cache has checked out, or "" when there is no
// Cache. It reads local git state only.
func (c Cache) head() string {
	return localRepoCommit(c.dir())
}

// headEntry is what a Cache's head commit holds at one subpath: the object
// ID, and whether it is a directory. The zero value is a subpath the commit
// does not have, or a Cache that is not there.
type headEntry struct {
	id  string
	dir bool
}

func (e headEntry) exists() bool {
	return e.id != ""
}

// atHead reads subpath in the head commit. It reads trees only, which a
// blobless clone always has, so it answers offline whatever the sparse
// checkout covers and never downloads the file it names.
func (c Cache) atHead(subpath string) headEntry {
	subpath = cleanSparsePaths([]string{subpath})[0]
	if subpath == "." {
		id, _, err := runGit(c.dir(), "rev-parse", "--verify", "--quiet", "HEAD^{tree}")
		if err != nil {
			return headEntry{}
		}
		return headEntry{id: id, dir: true}
	}
	// Each entry is "<mode> <type> <id>\t<path>"; -z leaves the path unquoted.
	stdout, _, err := runGit(c.dir(), "--literal-pathspecs", "ls-tree", "-z", "HEAD", "--", subpath)
	if err != nil {
		return headEntry{}
	}
	for entry := range strings.SplitSeq(stdout, "\x00") {
		meta, path, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if ok && path == subpath && len(fields) == 3 {
			return headEntry{id: fields[2], dir: fields[1] == "tree"}
		}
	}
	return headEntry{}
}

// coverage is this Cache's sparse checkout, which says which subpaths are on
// disk and which requested ones are missing. A Cache with no sparse checkout
// covers every subpath. It reads local git state only.
func (c Cache) coverage() (sparseState, error) {
	return readSparseState(c.dir())
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
	// cache is the Cache observed, on the default branch it resolved to.
	cache Cache
}

// observe reports git facts for this Cache. Classification into RemoteStatus
// is Freshness, not Cache.
func (c Cache) observe(paths ...string) cacheFacts {
	facts := cacheFacts{source: c.key}
	defaultRemoteSHA := ""
	if c.branch == "" && models.ParseRepoSource(c.key).Branch == "" {
		resolvedBranch, resolvedSHA, err := getRemoteDefaultBranchCommit(c.key, c.repo().URL)
		if err != nil {
			facts.err = err.Error()
		} else {
			c.branch = resolvedBranch
			defaultRemoteSHA = resolvedSHA
		}
	}
	repo := c.repo()
	facts.url, facts.branch, facts.dir, facts.cache = repo.URL, cmp.Or(repo.Branch, "HEAD"), repo.Dir, c
	facts.localSHA = c.head()
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
	coverage, err := c.coverage()
	if err != nil {
		facts.err = err.Error()
		return facts
	}
	facts.missing = coverage.missing(paths)
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
