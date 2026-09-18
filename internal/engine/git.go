package engine

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/akunzai/skills-manager/internal/models"
)

func cacheBranchKey(branch string) string {
	if branch == "" {
		branch = "HEAD"
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(branch)))[:16]
}

func defaultBranchMarkerPath(source, url, cacheDir string) string {
	identity := models.ParseRepoSource(source).SourceKey + "\x00" + url
	sum := sha256.Sum256([]byte(identity))
	return filepath.Join(cacheDirOrDefault(cacheDir), ".branch-identities", fmt.Sprintf("%x", sum[:])[:24])
}

func cachedDefaultBranch(source, url, cacheDir string) string {
	data, err := os.ReadFile(defaultBranchMarkerPath(source, url, cacheDir))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func recordDefaultBranch(source, url, cacheDir, branch string) error {
	path := defaultBranchMarkerPath(source, url, cacheDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(branch+"\n"), 0o644)
}

func GetRemoteDefaultBranch(source, url string) (string, error) {
	branch, _, err := getRemoteDefaultBranchCommit(source, url)
	return branch, err
}

func getRemoteDefaultBranchCommit(source, url string) (string, string, error) {
	repo := resolveCacheRepo(source, url, "", "")
	stdout, stderr, err := RunGit("", "ls-remote", "--symref", repo.URL, "HEAD")
	if err != nil {
		return "", "", gitOpErr("query default branch of", repo.URL, stdout, stderr, err)
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
		return "", "", fmt.Errorf("default branch not found for %s", repo.URL)
	}
	if commit == "" {
		return "", "", fmt.Errorf("default branch commit not found for %s", repo.URL)
	}
	return branch, commit, nil
}

// cacheRepo is one remote Source in the git Cache: key, clone URL, branch, dir.
type cacheRepo struct {
	SourceKey string
	URL       string
	Branch    string
	Dir       string
}

func cacheDirOrDefault(cacheDir string) string {
	if cacheDir == "" {
		return models.DefaultCacheDir()
	}
	return cacheDir
}

func resolveCacheRepo(source, url, branch, cacheDir string) cacheRepo {
	parsed := models.ParseRepoSource(source)
	repoURL := url
	if repoURL == "" {
		repoURL = parsed.URL
	}
	targetBranch := branch
	if targetBranch == "" {
		targetBranch = parsed.Branch
	}
	baseCache := cacheDirOrDefault(cacheDir)
	if targetBranch == "" {
		targetBranch = cachedDefaultBranch(parsed.SourceKey, repoURL, baseCache)
	}
	return cacheRepo{
		SourceKey: parsed.SourceKey,
		URL:       repoURL,
		Branch:    targetBranch,
		Dir:       filepath.Join(baseCache, filepath.FromSlash(parsed.SourceKey), cacheBranchKey(targetBranch)),
	}
}

func gitOpErr(action, repoURL, stdout, stderr string, err error) error {
	msg := stderr
	if msg == "" {
		msg = stdout
	}
	if msg == "" {
		msg = err.Error()
	}
	return fmt.Errorf("failed to %s %s: %s", action, repoURL, msg)
}

// RunGit executes a git command directly without passing through a shell.
// On Windows, Git defaults core.longpaths to false, which fails checkouts when
// path lengths exceed 260 characters (MAX_PATH). We inject -c core.longpaths=true
// to ensure transparent long-path support.
// References:
//   - https://git-scm.com/docs/git-config#Documentation/git-config.txt-corelongpaths
//   - https://learn.microsoft.com/windows/win32/fileio/maximum-file-path-limitation
func RunGit(cwd string, args ...string) (string, string, error) {
	cmdArgs := args
	if runtime.GOOS == "windows" {
		cmdArgs = append([]string{"-c", "core.longpaths=true"}, args...)
	}
	cmd := exec.Command("git", cmdArgs...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	// Disable terminal and Git Credential Manager GUI prompts so remote
	// queries fail cleanly rather than blocking or popping dialog windows.
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=never",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

// EnsureGitRepo clones or refreshes one Source's Cache and makes sure the
// declared Skill subpaths are in its sparse checkout. The Cache is a blobless,
// sparse clone so paths no Scope declares are never written to disk; only
// '.' (a Skill at the repository root) needs the whole tree.
func EnsureGitRepo(
	source string,
	url string,
	branch string,
	forceUpdate bool,
	cacheDir string,
	paths ...string,
) (string, error) {
	if err := checkGitVersion(); err != nil {
		return "", err
	}
	parsed := models.ParseRepoSource(source)
	requestedBranch := branch
	if requestedBranch == "" {
		requestedBranch = parsed.Branch
	}
	resolvedDefault := false
	if requestedBranch == "" {
		var err error
		requestedBranch, err = GetRemoteDefaultBranch(source, url)
		if err != nil {
			return "", err
		}
		resolvedDefault = true
	}
	repo := resolveCacheRepo(source, url, requestedBranch, cacheDir)

	gitDir := filepath.Join(repo.Dir, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		if runtime.GOOS == "windows" {
			_, _, _ = RunGit(repo.Dir, "config", "core.longpaths", "true")
		}
		if forceUpdate {
			// Narrow the working tree before the reset writes a new commit, so
			// a Cache cloned in full by an older release never checks out paths
			// no Scope declares.
			if err := normalizeSparseCheckout(repo.Dir, slices.Contains(cleanSparsePaths(paths), ".")); err != nil {
				return "", fmt.Errorf("prepare sparse checkout of %s: %w", repo.URL, err)
			}
			ref := repo.Branch
			if ref == "" {
				ref = "HEAD"
			}
			stdout, stderr, err := RunGit(repo.Dir, "fetch", "--depth", "1", "origin", ref)
			if err != nil {
				return "", gitOpErr("fetch", repo.URL, stdout, stderr, err)
			}
			stdout, stderr, err = RunGit(repo.Dir, "reset", "--hard", "FETCH_HEAD")
			if err != nil {
				return "", gitOpErr("reset", repo.URL, stdout, stderr, err)
			}
		}
		if err := ensureSparsePaths(repo.Dir, paths); err != nil {
			return "", fmt.Errorf("add Skills of %s to the Cache: %w", repo.URL, err)
		}
		if resolvedDefault {
			if err := recordDefaultBranch(source, repo.URL, cacheDir, requestedBranch); err != nil {
				return "", fmt.Errorf("record default branch: %w", err)
			}
		}
		return repo.Dir, nil
	}

	if err := os.MkdirAll(filepath.Dir(repo.Dir), 0755); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}
	_ = RemoveAll(repo.Dir)

	// --filter=blob:none defers file contents until checkout needs them and
	// --sparse starts the working tree at the root files only.
	// https://git-scm.com/docs/partial-clone
	// https://git-scm.com/docs/git-sparse-checkout
	cloneArgs := []string{"clone", "--depth", "1", "--filter=blob:none", "--sparse"}
	if runtime.GOOS == "windows" {
		cloneArgs = append(cloneArgs, "-c", "core.longpaths=true")
	}
	if repo.Branch != "" {
		cloneArgs = append(cloneArgs, "--branch", repo.Branch)
	}
	cloneArgs = append(cloneArgs, repo.URL, repo.Dir)

	stdout, stderr, err := RunGit("", cloneArgs...)
	if err != nil {
		return "", gitOpErr("clone", repo.URL, stdout, stderr, err)
	}
	if err := markSparseCache(repo.Dir); err != nil {
		return "", err
	}
	if err := ensureSparsePaths(repo.Dir, paths); err != nil {
		return "", fmt.Errorf("add Skills of %s to the Cache: %w", repo.URL, err)
	}
	if resolvedDefault {
		if err := recordDefaultBranch(source, repo.URL, cacheDir, requestedBranch); err != nil {
			return "", fmt.Errorf("record default branch: %w", err)
		}
	}

	return repo.Dir, nil
}

// Git 2.35 unified 'sparse-checkout init' into 'set', which is what lets the
// Cache switch between --cone and --no-cone (see withSkillFiles).
// https://github.com/git/git/blob/master/Documentation/RelNotes/2.35.0.adoc
const minGitMajor, minGitMinor = 2, 35

// gitVersionErr is a variable so a test can stand in for an unsupported git;
// no engine test runs in parallel, so swapping it is race-free.
var gitVersionErr = sync.OnceValue(func() error {
	stdout, stderr, err := RunGit("", "version")
	if err != nil {
		return gitOpErr("run", "git version", stdout, stderr, err)
	}
	major, minor, ok := parseGitVersion(stdout)
	if !ok {
		return fmt.Errorf("unrecognized git version %q", stdout)
	}
	if major < minGitMajor || (major == minGitMajor && minor < minGitMinor) {
		return fmt.Errorf("git %d.%d or newer is required for the sparse Cache; found %q", minGitMajor, minGitMinor, stdout)
	}
	return nil
})

// checkGitVersion reports whether the git on PATH can maintain the sparse Cache.
func checkGitVersion() error {
	return gitVersionErr()
}

// parseGitVersion reads "git version 2.45.1" and vendor suffixes such as
// "git version 2.45.1.windows.1" or "git version 2.39.5 (Apple Git-154)".
func parseGitVersion(output string) (int, int, bool) {
	fields := strings.Fields(output)
	if len(fields) < 3 || fields[0] != "git" || fields[1] != "version" {
		return 0, 0, false
	}
	parts := strings.Split(fields[2], ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// sparseCacheKey marks a Cache this release cloned or converted. Without it, a
// Cache with no sparse checkout is a full clone from an earlier release; with
// it, the full checkout is deliberate, because a Scope declares the root.
const sparseCacheKey = "skills-manager.sparseCache"

func markSparseCache(repoDir string) error {
	stdout, stderr, err := RunGit(repoDir, "config", sparseCacheKey, "true")
	if err != nil {
		return gitOpErr("mark sparse Cache", repoDir, stdout, stderr, err)
	}
	return nil
}

// sparseState is a Cache's sparse-checkout definition. Disabled means the
// whole tree is checked out.
type sparseState struct {
	enabled bool
	cone    bool
	dirs    []string
	// marked is set once this release has cloned or converted the Cache.
	marked bool
}

func readSparseState(repoDir string) (sparseState, error) {
	marked, _, _ := RunGit(repoDir, "config", "--bool", sparseCacheKey)
	enabled, _, _ := RunGit(repoDir, "config", "--bool", "core.sparseCheckout")
	if enabled != "true" {
		return sparseState{marked: marked == "true"}, nil
	}
	cone, _, _ := RunGit(repoDir, "config", "--bool", "core.sparseCheckoutCone")
	stdout, stderr, err := RunGit(repoDir, "sparse-checkout", "list")
	if err != nil {
		return sparseState{}, gitOpErr("list sparse checkout of", repoDir, stdout, stderr, err)
	}
	state := sparseState{enabled: true, cone: cone == "true", marked: marked == "true"}
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !state.cone {
			// Only the directory patterns checkOutSkillFiles writes carry a
			// cone directory; the rest are its root and SKILL.md patterns.
			dir, ok := unescapeDirPattern(line)
			if !ok {
				continue
			}
			line = dir
		}
		state.dirs = append(state.dirs, line)
	}
	return state, nil
}

// escapeDirPattern renders a cone directory as a non-cone pattern that
// matches it literally. https://git-scm.com/docs/gitignore#_pattern_format
func escapeDirPattern(dir string) string {
	var b strings.Builder
	b.WriteByte('/')
	for _, r := range dir {
		if strings.ContainsRune(`\*?[!#`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('/')
	return b.String()
}

// unescapeDirPattern reverses escapeDirPattern, rejecting any pattern with an
// unescaped wildcard, which escapeDirPattern never writes.
func unescapeDirPattern(pattern string) (string, bool) {
	if len(pattern) < 3 || pattern[0] != '/' || pattern[len(pattern)-1] != '/' {
		return "", false
	}
	var b strings.Builder
	escaped := false
	for _, r := range pattern[1 : len(pattern)-1] {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case strings.ContainsRune("*?[", r):
			return "", false
		default:
			b.WriteRune(r)
		}
	}
	return b.String(), !escaped
}

func (s sparseState) covers(subpath string) bool {
	if !s.enabled {
		return true
	}
	if !s.cone || subpath == "." {
		return false
	}
	for _, dir := range s.dirs {
		if subpath == dir || strings.HasPrefix(subpath, dir+"/") {
			return true
		}
	}
	return false
}

func cleanSparsePaths(paths []string) []string {
	cleaned := make([]string, 0, len(paths))
	for _, p := range paths {
		p = path.Clean(filepath.ToSlash(p))
		if p == "/" || p == "" {
			p = "."
		}
		cleaned = append(cleaned, strings.TrimPrefix(p, "/"))
	}
	slices.Sort(cleaned)
	return slices.Compact(cleaned)
}

func (s sparseState) missing(paths []string) []string {
	var missing []string
	for _, p := range cleanSparsePaths(paths) {
		if !s.covers(p) {
			missing = append(missing, p)
		}
	}
	return missing
}

// missingSparsePaths lists the subpaths a Cache's sparse checkout does not
// cover. It reads local git state only, so offline Freshness can use it.
func missingSparsePaths(repoDir string, paths []string) ([]string, error) {
	state, err := readSparseState(repoDir)
	if err != nil {
		return nil, err
	}
	return state.missing(paths), nil
}

// normalizeSparseCheckout puts a Cache in cone mode before a refresh. A Cache
// cloned in full by an older release is narrowed to its root files, unless a
// Scope declares the repository root; a Cache left in discovery mode by an
// interrupted add returns to the cone it had.
func normalizeSparseCheckout(repoDir string, keepFull bool) error {
	state, err := readSparseState(repoDir)
	if err != nil {
		return err
	}
	switch {
	case state.enabled && state.cone:
		return nil
	case !state.enabled && keepFull:
		return markSparseCache(repoDir)
	case !state.enabled && state.marked:
		// A marked Cache without a sparse checkout is a root Skill's full
		// tree, which another Scope may need: the sparse checkout only grows.
		return nil
	}
	if err := setSparseCone(repoDir, state.dirs); err != nil {
		return err
	}
	return markSparseCache(repoDir)
}

func setSparseCone(repoDir string, dirs []string) error {
	args := append([]string{"sparse-checkout", "set", "--cone"}, dirs...)
	stdout, stderr, err := RunGit(repoDir, args...)
	if err != nil {
		return gitOpErr("set sparse checkout of", repoDir, stdout, stderr, err)
	}
	return nil
}

// ensureSparsePaths adds subpaths to a Cache's sparse checkout without
// removing any: the Cache is shared by every Scope, and this run only sees
// its own Config (ADR 0004). A Cache without a sparse checkout already has
// every path.
func ensureSparsePaths(repoDir string, paths []string) error {
	paths = cleanSparsePaths(paths)
	if len(paths) == 0 {
		return nil
	}
	state, err := readSparseState(repoDir)
	if err != nil || !state.enabled {
		return err
	}
	if slices.Contains(paths, ".") {
		stdout, stderr, err := RunGit(repoDir, "sparse-checkout", "disable")
		if err != nil {
			return gitOpErr("disable sparse checkout of", repoDir, stdout, stderr, err)
		}
		return nil
	}
	if !state.cone {
		if err := setSparseCone(repoDir, state.dirs); err != nil {
			return err
		}
		state.cone = true
	}
	missing := state.missing(paths)
	if len(missing) == 0 {
		return nil
	}
	stdout, stderr, err := RunGit(repoDir, append([]string{"sparse-checkout", "add"}, missing...)...)
	if err != nil {
		return gitOpErr("add sparse checkout paths to", repoDir, stdout, stderr, err)
	}
	return nil
}

// checkOutSkillFiles switches the Cache to non-cone mode: its root files,
// the cone directories it had, and every SKILL.md. readSparseState recovers
// those cone directories if the switch back never happens.
func checkOutSkillFiles(repoDir string, state sparseState) error {
	patterns := []string{"/*", "!/*/"}
	for _, dir := range state.dirs {
		patterns = append(patterns, escapeDirPattern(dir))
	}
	// SKILL.md is matched case-insensitively, as DiscoverSkillsInRepo does.
	patterns = append(patterns, "[Ss][Kk][Ii][Ll][Ll].[Mm][Dd]")
	stdout, stderr, err := RunGit(repoDir, append([]string{"sparse-checkout", "set", "--no-cone"}, patterns...)...)
	if err != nil {
		return gitOpErr("check out SKILL.md files in", repoDir, stdout, stderr, err)
	}
	return nil
}

// withSkillFiles checks out every SKILL.md in the Cache, alongside the cone
// it already has, while discover runs, then restores that cone. Add needs
// every Skill's SKILL.md before the user picks any; checking out whole
// Skill directories would write files Windows may not accept (ADR 0004).
func withSkillFiles(repoDir string, discover func() error) error {
	state, err := readSparseState(repoDir)
	if err != nil {
		return err
	}
	if !state.enabled {
		return discover()
	}
	if err := checkOutSkillFiles(repoDir, state); err != nil {
		return err
	}
	discoverErr := discover()
	if err := setSparseCone(repoDir, state.dirs); err != nil {
		return errors.Join(discoverErr, err)
	}
	return discoverErr
}

func GetLocalRepoCommit(repoDest string) string {
	gitDir := filepath.Join(repoDest, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		return ""
	}
	stdout, _, err := RunGit(repoDest, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(stdout)
}

func GetRemoteRepoCommitResult(source, url, branch string) (string, error) {
	repo := resolveCacheRepo(source, url, branch, "")
	refTarget := repo.Branch
	if refTarget == "" {
		refTarget = "HEAD"
	} else {
		refTarget = "refs/heads/" + refTarget
	}

	stdout, stderr, err := RunGit("", "ls-remote", repo.URL, refTarget)
	if err != nil || stdout == "" {
		if err != nil {
			return "", gitOpErr("query", repo.URL, stdout, stderr, err)
		}
		return "", fmt.Errorf("remote ref %s not found in %s", refTarget, repo.URL)
	}

	lines := strings.Split(stdout, "\n")
	if len(lines) > 0 {
		parts := strings.Fields(lines[0])
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0]), nil
		}
	}
	return "", fmt.Errorf("invalid remote response from %s", repo.URL)
}
