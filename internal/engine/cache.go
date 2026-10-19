package engine

import (
	"cmp"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/akunzai/skills-manager/internal/models"
)

// Cache is one remote Source's working copy. It stores Source identity, not
// Config: callers pass the Skill subpaths each operation should cover.
type Cache struct {
	source   string
	key      string
	url      string
	branch   string
	cacheDir string
}

func NewCache(source, url, branch, cacheDir string) Cache {
	parsed := models.ParseRepoSource(source)
	return Cache{
		source:   source,
		key:      parsed.SourceKey,
		url:      cmp.Or(url, parsed.URL),
		branch:   cmp.Or(strings.TrimSpace(branch), parsed.Branch),
		cacheDir: cacheDirOrDefault(cacheDir),
	}
}

func cacheDirOrDefault(cacheDir string) string {
	if cacheDir == "" {
		return models.DefaultCacheDir()
	}
	return cacheDir
}

func cacheBranchKey(branch string) string {
	if branch == "" {
		branch = "HEAD"
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(branch)))[:16]
}

func (c Cache) withBranch(branch string) Cache {
	c.branch = strings.TrimSpace(branch)
	return c
}

func (c Cache) defaultBranchMarkerPath() string {
	identity := c.key + "\x00" + c.url
	sum := sha256.Sum256([]byte(identity))
	return filepath.Join(c.cacheDir, ".branch-identities", fmt.Sprintf("%x", sum[:])[:24])
}

func (c Cache) cachedDefaultBranch() string {
	data, err := os.ReadFile(c.defaultBranchMarkerPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (c Cache) recordDefaultBranch(branch string) error {
	path := c.defaultBranchMarkerPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(branch+"\n"), 0o644)
}

func (c Cache) resolvedBranch() string {
	return cmp.Or(c.branch, c.cachedDefaultBranch())
}

func (c Cache) dirForBranch(branch string) string {
	return filepath.Join(c.cacheDir, filepath.FromSlash(c.key), cacheBranchKey(branch))
}

func (c Cache) dir() string {
	return c.dirForBranch(c.resolvedBranch())
}

// Refresh clones or fetches this Cache and covers paths in its sparse checkout.
func (c Cache) Refresh(force bool, paths ...string) error {
	return c.RefreshWithProgress(force, nil, paths...)
}

// RefreshWithProgress clones or fetches this Cache and covers paths while emitting progress.
func (c Cache) RefreshWithProgress(force bool, progress UpdateProgress, paths ...string) error {
	if err := checkGitVersion(); err != nil {
		return err
	}
	if c.branch != "" {
		return c.checkout(force, paths)
	}
	previousDefault := c.cachedDefaultBranch()
	remoteDefault, _, err := c.remoteDefaultBranchCommit()
	if err != nil {
		return err
	}
	target := c.withBranch(remoteDefault)
	if previousDefault != "" && remoteDefault != previousDefault {
		if err := target.checkout(force, paths); err != nil {
			return err
		}
		missing := slices.ContainsFunc(paths, func(p string) bool {
			return !target.atHead(p).exists()
		})
		if missing {
			prev := c.withBranch(previousDefault)
			if _, err := os.Stat(filepath.Join(prev.dir(), ".git")); err == nil {
				_ = RemoveAll(target.dir())
				emitUpdate(progress, UpdateEvent{
					Kind:   UpdateBranchMissingWarn,
					Source: c.source,
					From:   previousDefault,
					To:     remoteDefault,
				})
				return prev.checkout(force, paths)
			}
			if err := c.recordDefaultBranch(remoteDefault); err != nil {
				return fmt.Errorf("record default branch: %w", err)
			}
			return nil
		}
		emitUpdate(progress, UpdateEvent{
			Kind:   UpdateBranchChangedNotice,
			Source: c.source,
			From:   previousDefault,
			To:     remoteDefault,
		})
		if err := c.recordDefaultBranch(remoteDefault); err != nil {
			return fmt.Errorf("record default branch: %w", err)
		}
		return nil
	}
	if err := target.checkout(force, paths); err != nil {
		return err
	}
	if err := c.recordDefaultBranch(remoteDefault); err != nil {
		return fmt.Errorf("record default branch: %w", err)
	}
	return nil
}

// checkout clones or refreshes one branch working copy of this Source's Cache
// and makes sure the declared Skill subpaths are in its sparse checkout. The
// Cache is a blobless, sparse clone so paths no Scope declares are never
// written to disk; only '.' (a Skill at the repository root) needs the whole tree.
func (c Cache) checkout(forceUpdate bool, paths []string) error {
	repoDir := c.dir()
	gitDir := filepath.Join(repoDir, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		if runtime.GOOS == "windows" {
			_, _, _ = runGit(repoDir, "config", "core.longpaths", "true")
		}
		if forceUpdate {
			// Narrow the working tree before the reset writes a new commit, so
			// a Cache cloned in full by an older release never checks out paths
			// no Scope declares.
			if err := normalizeSparseCheckout(repoDir, slices.Contains(cleanSparsePaths(paths), ".")); err != nil {
				return fmt.Errorf("prepare sparse checkout of %s: %w", c.url, err)
			}
			ref := cmp.Or(c.resolvedBranch(), "HEAD")
			stdout, stderr, err := runGit(repoDir, "fetch", "--depth", "1", "origin", ref)
			if err != nil {
				return gitOpErr("fetch", c.url, stdout, stderr, err)
			}
			stdout, stderr, err = runGit(repoDir, "reset", "--hard", "FETCH_HEAD")
			if err != nil {
				return gitOpErr("reset", c.url, stdout, stderr, err)
			}
		}
		if err := ensureSparsePaths(repoDir, paths); err != nil {
			return fmt.Errorf("add Skills of %s to the Cache: %w", c.url, err)
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}
	_ = RemoveAll(repoDir)

	// --filter=blob:none defers file contents until checkout needs them and
	// --sparse starts the working tree at the root files only.
	// https://git-scm.com/docs/partial-clone
	// https://git-scm.com/docs/git-sparse-checkout
	cloneArgs := []string{"clone", "--depth", "1", "--filter=blob:none", "--sparse"}
	if runtime.GOOS == "windows" {
		cloneArgs = append(cloneArgs, "-c", "core.longpaths=true")
	}
	if branch := c.resolvedBranch(); branch != "" {
		cloneArgs = append(cloneArgs, "--branch", branch)
	}
	cloneArgs = append(cloneArgs, c.url, repoDir)

	stdout, stderr, err := runGit("", cloneArgs...)
	if err != nil {
		return gitOpErr("clone", c.url, stdout, stderr, err)
	}
	if err := markSparseCache(repoDir); err != nil {
		return err
	}
	if err := ensureSparsePaths(repoDir, paths); err != nil {
		return fmt.Errorf("add Skills of %s to the Cache: %w", c.url, err)
	}
	return nil
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

func (c Cache) remoteDefaultBranchCommit() (string, string, error) {
	stdout, stderr, err := runGit("", "ls-remote", "--symref", c.url, "HEAD")
	if err != nil {
		return "", "", gitOpErr("query default branch of", c.url, stdout, stderr, err)
	}
	branch := ""
	commit := ""
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "ref:" && fields[2] == "HEAD" {
			branch = strings.TrimPrefix(fields[1], "refs/heads/")
		}
		if len(fields) >= 2 && fields[1] == "HEAD" && fields[0] != "ref:" {
			commit = fields[0]
		}
	}
	if branch == "" {
		return "", "", fmt.Errorf("default branch not found for %s", c.url)
	}
	if commit == "" {
		return "", "", fmt.Errorf("default branch commit not found for %s", c.url)
	}
	return branch, commit, nil
}

func (c Cache) remoteCommit() (string, error) {
	branch := c.resolvedBranch()
	if branch == "" {
		return lsRemoteCommit(c.url, "HEAD")
	}
	// A declared branch may name a tag (add --branch v1.2.0). A branch wins
	// when both exist; an annotated tag is peeled to the commit the Cache
	// checks out, so Freshness compares like with like.
	refs, err := lsRemote(c.url, "refs/heads/"+branch, "refs/tags/"+branch, "refs/tags/"+branch+"^{}")
	if err != nil {
		return "", err
	}
	for _, ref := range []string{"refs/heads/" + branch, "refs/tags/" + branch + "^{}", "refs/tags/" + branch} {
		if sha, ok := refs[ref]; ok {
			return sha, nil
		}
	}
	return "", fmt.Errorf("remote branch or tag %s not found in %s", branch, c.url)
}

type cacheFacts struct {
	source        string
	url           string
	branch        string
	dir           string
	localSHA      string
	remoteSHA     string
	branchChanged bool
	missing       []string
	// changed names the Skills whose version differs between the Cache and
	// the Source, when the Source says so per Skill.
	changed []string
	err     string
	// cache is the Cache observed, on the branch its local checkout uses.
	cache sourceCache
}

// observe reports git facts for this Cache. Classification into RemoteStatus
// is Freshness, not Cache.
func (c Cache) observe(paths ...string) cacheFacts {
	facts := cacheFacts{source: c.source, url: c.url, cache: c}
	if c.branch == "" {
		cachedBranch := c.cachedDefaultBranch()
		resolvedBranch, resolvedSHA, err := c.remoteDefaultBranchCommit()
		if err != nil {
			facts.err = err.Error()
			facts.branch = cmp.Or(cachedBranch, "HEAD")
			facts.dir = c.dirForBranch(cachedBranch)
			facts.localSHA = localRepoCommit(facts.dir)
			return facts
		}
		facts.branch = resolvedBranch
		facts.remoteSHA = resolvedSHA
		localBranch := resolvedBranch
		if cachedBranch != "" && localRepoCommit(c.dirForBranch(cachedBranch)) != "" {
			localBranch = cachedBranch
			facts.branchChanged = cachedBranch != resolvedBranch
		} else {
			facts.cache = c.withBranch(resolvedBranch)
		}
		facts.dir = c.dirForBranch(localBranch)
		facts.localSHA = localRepoCommit(facts.dir)
		if facts.localSHA == "" || facts.localSHA != facts.remoteSHA || facts.branchChanged {
			return facts
		}
		coverage, err := facts.cache.coverage()
		if err != nil {
			facts.err = err.Error()
			return facts
		}
		facts.missing = coverage.missing(paths)
		return facts
	}

	facts.branch = c.branch
	facts.dir = c.dir()
	facts.localSHA = c.head()
	if facts.localSHA == "" {
		return facts
	}
	remoteSHA, remoteErr := c.remoteCommit()
	if remoteErr != nil {
		facts.err = remoteErr.Error()
		return facts
	}
	facts.remoteSHA = remoteSHA
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

// patch is the patch of subpath from one commit of this Cache to
// another, with paths relative to subpath. A blob the partial clone never
// fetched is fetched on demand, which is the only way this reaches the network.
func (c Cache) patch(from, to, subpath string) (string, error) {
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

// blob reads the file at path, relative to the repository root, as
// commit has it. A symlink reads as its target, as DigestSkillContent hashes it.
func (c Cache) blob(commit, path string) ([]byte, error) {
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
