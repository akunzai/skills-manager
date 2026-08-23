package engine

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/akunzai/skills-manager/internal/models"
)

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
	return cacheRepo{
		SourceKey: parsed.SourceKey,
		URL:       repoURL,
		Branch:    targetBranch,
		Dir:       filepath.Join(baseCache, filepath.FromSlash(parsed.SourceKey)),
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
	repo := resolveCacheRepo(source, url, branch, cacheDir)

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

func GetRemoteRepoCommit(source, url, branch string) string {
	repo := resolveCacheRepo(source, url, branch, "")
	refTarget := repo.Branch
	if refTarget == "" {
		refTarget = "HEAD"
	}

	stdout, _, err := RunGit("", "ls-remote", repo.URL, refTarget)
	if err != nil || stdout == "" {
		return ""
	}

	lines := strings.Split(stdout, "\n")
	if len(lines) > 0 {
		parts := strings.Fields(lines[0])
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	return ""
}
