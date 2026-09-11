package engine

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/akunzai/skills-manager/internal/models"
)

var errRepoPathMissing = errors.New("path missing in repository")

func RunCmd(cmdStr string, cwd string) (string, string, error) {
	var cmd *exec.Cmd
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

// MaterializeRemoteSkill copies one Skill from a cached repository into the
// Scope skills directory.
func MaterializeRemoteSkill(name, subpath, repoDir, skillsDir string) error {
	srcPath := filepath.Join(repoDir, filepath.FromSlash(subpath))
	if _, err := os.Stat(srcPath); err != nil {
		return fmt.Errorf("%w: %s", errRepoPathMissing, subpath)
	}
	return CopySkillFolder(srcPath, filepath.Join(skillsDir, name))
}

// MaterializeLocalSymlink points the Scope skills directory entry at a local
// Skill directory. linkTarget is the path written into the symlink (relative
// or absolute); the caller resolves Scope portability. A Source that resolves
// inside the skills directory is occupancy, not a Source: refuse before
// CreateSymlink would RemoveAll the destination.
func MaterializeLocalSymlink(name, linkTarget, skillsDir string) error {
	dest := filepath.Join(skillsDir, name)
	absSource := linkTarget
	if !filepath.IsAbs(absSource) {
		absSource = filepath.Join(filepath.Dir(dest), absSource)
	}
	if models.LocalSourceInsideSkillsDir(absSource, skillsDir) {
		return fmt.Errorf("local source %s is inside the skills directory", models.ToTildePath(absSource))
	}
	return CreateSymlink(linkTarget, dest, true)
}

// MaterializeCommand runs the installer for a command Source. A failed check
// is the caller's decision to skip; this only runs the installer command.
func MaterializeCommand(command string) error {
	stdout, stderr, err := RunCmd(command, "")
	if err == nil {
		return nil
	}
	msg := stderr
	if msg == "" {
		msg = stdout
	}
	if msg == "" {
		msg = err.Error()
	}
	return fmt.Errorf("%s", msg)
}
