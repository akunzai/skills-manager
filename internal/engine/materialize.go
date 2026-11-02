package engine

import (
	"bytes"
	"cmp"
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

// MaterializeCommandOutput runs a command Source whose stdout is its Skill's
// SKILL.md and writes that output to the Scope skills directory, supplying
// frontmatter when the command printed none. A command that fails or prints
// nothing leaves the Scope copy as it was.
func MaterializeCommandOutput(command, skillsDir, name, description string) error {
	stdout, stderr, err := RunCmd(command, "")
	if err != nil {
		return fmt.Errorf("%s", cmp.Or(stderr, stdout, err.Error()))
	}
	if stdout == "" {
		return fmt.Errorf("command printed nothing to write as %s's SKILL.md", name)
	}
	skillDir := filepath.Join(skillsDir, name)
	// The Skill is a directory this command owns; a link or file in its
	// place is replaced rather than written through.
	if info, err := os.Lstat(skillDir); err == nil && !info.IsDir() {
		if err := os.Remove(skillDir); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return err
	}
	content := withSkillFrontmatter(stdout, name, description) + "\n"
	tmp, err := os.CreateTemp(skillDir, ".SKILL.md-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(skillDir, "SKILL.md"))
}
