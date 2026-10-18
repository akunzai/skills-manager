package engine

import (
	"bytes"
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
)

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

// runGit executes a git command directly without passing through a shell.
// On Windows, Git defaults core.longpaths to false, which fails checkouts when
// path lengths exceed 260 characters (MAX_PATH). We inject -c core.longpaths=true
// to ensure transparent long-path support.
// References:
//   - https://git-scm.com/docs/git-config#Documentation/git-config.txt-corelongpaths
//   - https://learn.microsoft.com/windows/win32/fileio/maximum-file-path-limitation
func runGit(cwd string, args ...string) (string, string, error) {
	stdout, stderr, err := runGitRaw(cwd, args...)
	return strings.TrimSpace(string(stdout)), stderr, err
}

// runGitRaw is runGit for output whose bytes matter, such as a blob or a
// patch, where trimming would drop a trailing blank line.
func runGitRaw(cwd string, args ...string) ([]byte, string, error) {
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
	return stdout.Bytes(), strings.TrimSpace(stderr.String()), err
}

// Git 2.35 unified 'sparse-checkout init' into 'set', which is what lets the
// Cache switch between --cone and --no-cone (see withSkillFiles).
// https://github.com/git/git/blob/master/Documentation/RelNotes/2.35.0.adoc
const minGitMajor, minGitMinor = 2, 35

// gitVersionErr is a variable so a test can stand in for an unsupported git;
// no engine test runs in parallel, so swapping it is race-free.
var gitVersionErr = sync.OnceValue(func() error {
	stdout, stderr, err := runGit("", "version")
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
	stdout, stderr, err := runGit(repoDir, "config", sparseCacheKey, "true")
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
	marked, _, _ := runGit(repoDir, "config", "--bool", sparseCacheKey)
	enabled, _, _ := runGit(repoDir, "config", "--bool", "core.sparseCheckout")
	if enabled != "true" {
		return sparseState{marked: marked == "true"}, nil
	}
	cone, _, _ := runGit(repoDir, "config", "--bool", "core.sparseCheckoutCone")
	stdout, stderr, err := runGit(repoDir, "sparse-checkout", "list")
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
	stdout, stderr, err := runGit(repoDir, args...)
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
		stdout, stderr, err := runGit(repoDir, "sparse-checkout", "disable")
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
	stdout, stderr, err := runGit(repoDir, append([]string{"sparse-checkout", "add"}, missing...)...)
	if err != nil {
		return gitOpErr("add sparse checkout paths to", repoDir, stdout, stderr, err)
	}
	return nil
}

// committedSymlinks reports a SKILL.md under repoDir that its head commit
// records as a symlink. Such a file is an editor's mirror of a Skill
// elsewhere: it cannot be verified (ADR-0010) and breaks once Materialized.
// The commit decides rather than the file on disk, because with
// core.symlinks=false (the Windows default) git checks a symlink out as a
// plain file holding its target. It reads trees only, so it stays offline.
func committedSymlinks(repoDir string) func(path string) bool {
	links := make(map[string]bool)
	// Each entry is "<mode> <type> <id>\t<path>"; -z leaves the path unquoted.
	stdout, _, err := runGit(repoDir, "ls-tree", "-r", "-z", "HEAD")
	if err == nil {
		for entry := range strings.SplitSeq(stdout, "\x00") {
			meta, name, ok := strings.Cut(entry, "\t")
			if ok && strings.HasPrefix(meta, "120000 ") && strings.EqualFold(path.Base(name), "SKILL.md") {
				links[name] = true
			}
		}
	}
	return func(file string) bool {
		rel, err := filepath.Rel(repoDir, file)
		return err == nil && links[filepath.ToSlash(rel)]
	}
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
	stdout, stderr, err := runGit(repoDir, append([]string{"sparse-checkout", "set", "--no-cone"}, patterns...)...)
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

func localRepoCommit(repoDest string) string {
	gitDir := filepath.Join(repoDest, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		return ""
	}
	stdout, _, err := runGit(repoDest, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(stdout)
}

func lsRemoteCommit(url, ref string) (string, error) {
	refs, err := lsRemote(url, ref)
	if err != nil {
		return "", err
	}
	sha, ok := refs[ref]
	if !ok {
		return "", fmt.Errorf("remote ref %s not found in %s", ref, url)
	}
	return sha, nil
}

// lsRemote returns the commit each matching ref points at, keyed by ref name.
func lsRemote(url string, patterns ...string) (map[string]string, error) {
	stdout, stderr, err := runGit("", append([]string{"ls-remote", url}, patterns...)...)
	if err != nil {
		return nil, gitOpErr("query", url, stdout, stderr, err)
	}
	refs := make(map[string]string)
	for _, line := range strings.Split(stdout, "\n") {
		if parts := strings.Fields(line); len(parts) == 2 {
			refs[parts[1]] = parts[0]
		}
	}
	return refs, nil
}
