package engine

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
func RunGit(cwd string, args ...string) (string, string, error) {
	cmd := exec.Command("git", args...)
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

func EnsureGitRepo(
	source string,
	url string,
	branch string,
	forceUpdate bool,
	cacheDir string,
) (string, error) {
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
		if forceUpdate {
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

	cloneArgs := []string{"clone", "--depth", "1"}
	if repo.Branch != "" {
		cloneArgs = append(cloneArgs, "--branch", repo.Branch)
	}
	cloneArgs = append(cloneArgs, repo.URL, repo.Dir)

	stdout, stderr, err := RunGit("", cloneArgs...)
	if err != nil {
		return "", gitOpErr("clone", repo.URL, stdout, stderr, err)
	}
	if resolvedDefault {
		if err := recordDefaultBranch(source, repo.URL, cacheDir, requestedBranch); err != nil {
			return "", fmt.Errorf("record default branch: %w", err)
		}
	}

	return repo.Dir, nil
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
