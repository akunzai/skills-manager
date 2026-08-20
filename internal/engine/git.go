package engine

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/akunzai/skills-manager/internal/models"
)

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

func RunCmd(cmdStr string, cwd string) (string, string, error) {
	var cmd *exec.Cmd
	// Use sh on unix or cmd.exe on windows
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd.exe", "/c", cmdStr)
	} else {
		cmd = exec.Command("sh", "-c", cmdStr)
	}

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
	parsed := models.ParseRepoSource(source)
	cleanSource := parsed.SourceKey
	repoURL := url
	if repoURL == "" {
		repoURL = parsed.URL
	}
	targetBranch := branch
	if targetBranch == "" {
		targetBranch = parsed.Branch
	}

	baseCache := cacheDir
	if baseCache == "" {
		baseCache = models.DefaultCacheDir()
	}
	repoDest := filepath.Join(baseCache, filepath.FromSlash(cleanSource))

	gitDir := filepath.Join(repoDest, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		if forceUpdate {
			ref := targetBranch
			if ref == "" {
				ref = "HEAD"
			}
			stdout, stderr, err := RunGit(repoDest, "fetch", "--depth", "1", "origin", ref)
			if err != nil {
				errMsg := stderr
				if errMsg == "" {
					errMsg = stdout
				}
				if errMsg == "" {
					errMsg = err.Error()
				}
				return "", fmt.Errorf("failed to fetch %s: %s", repoURL, errMsg)
			}
			stdout, stderr, err = RunGit(repoDest, "reset", "--hard", "FETCH_HEAD")
			if err != nil {
				errMsg := stderr
				if errMsg == "" {
					errMsg = stdout
				}
				if errMsg == "" {
					errMsg = err.Error()
				}
				return "", fmt.Errorf("failed to reset %s: %s", repoURL, errMsg)
			}
		}
		return repoDest, nil
	}

	// Clone new
	if err := os.MkdirAll(filepath.Dir(repoDest), 0755); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}
	_ = RemoveAll(repoDest)

	cloneArgs := []string{"clone", "--depth", "1"}
	if targetBranch != "" {
		cloneArgs = append(cloneArgs, "--branch", targetBranch)
	}
	cloneArgs = append(cloneArgs, repoURL, repoDest)

	stdout, stderr, err := RunGit("", cloneArgs...)
	if err != nil {
		errMsg := stderr
		if errMsg == "" {
			errMsg = stdout
		}
		if errMsg == "" {
			errMsg = err.Error()
		}
		return "", fmt.Errorf("failed to clone %s: %s", repoURL, errMsg)
	}

	return repoDest, nil
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
	parsed := models.ParseRepoSource(source)
	repoURL := url
	if repoURL == "" {
		repoURL = parsed.URL
	}
	refTarget := branch
	if refTarget == "" {
		refTarget = parsed.Branch
	}
	if refTarget == "" {
		refTarget = "HEAD"
	}

	stdout, _, err := RunGit("", "ls-remote", repoURL, refTarget)
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
