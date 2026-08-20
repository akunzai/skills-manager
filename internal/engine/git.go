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

func RunCmd(cmdStr string, cwd string) (string, string, error) {
	var cmd *exec.Cmd
	// Use sh on unix or cmd.exe on windows
	if os.Getenv("OS") == "Windows_NT" {
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
			fetchCmd := fmt.Sprintf("git fetch --depth 1 origin %s", ref)
			_, _, _ = RunCmd(fetchCmd, repoDest)
			_, _, _ = RunCmd("git reset --hard FETCH_HEAD", repoDest)
		}
		return repoDest, nil
	}

	// Clone new
	if err := os.MkdirAll(filepath.Dir(repoDest), 0755); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}
	_ = os.RemoveAll(repoDest)

	branchFlag := ""
	if targetBranch != "" {
		branchFlag = fmt.Sprintf("--branch %s", targetBranch)
	}
	cloneCmd := fmt.Sprintf("git clone --depth 1 %s %s %q", branchFlag, repoURL, repoDest)
	stdout, stderr, err := RunCmd(cloneCmd, "")
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
	stdout, _, err := RunCmd("git rev-parse HEAD", repoDest)
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

	cmdStr := fmt.Sprintf("git ls-remote %s %s", repoURL, refTarget)
	stdout, _, err := RunCmd(cmdStr, "")
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
