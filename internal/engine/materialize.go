package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var errRepoPathMissing = errors.New("path missing in repository")

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
// or absolute); the caller resolves Scope portability.
func MaterializeLocalSymlink(name, linkTarget, skillsDir string) error {
	return CreateSymlink(linkTarget, filepath.Join(skillsDir, name), true)
}
