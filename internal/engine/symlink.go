package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/akunzai/skills-manager/internal/models"
)

const managedCopyMarker = ".skills-manager-copy"

// ErrLinkPrivilegeNotHeld is the Windows ERROR_PRIVILEGE_NOT_HELD status, "a
// required privilege is not held by the client": what the operating system
// answers when Developer Mode is off and the process is not elevated. It is
// written as a numeric literal rather than imported as
// golang.org/x/sys/windows.ERROR_PRIVILEGE_NOT_HELD because that constant
// lives in a Windows-only package, and importing it would force a build
// constraint onto this file — compiling the copy fallback out on every other
// platform and with it the only way to test it. This is the one place where
// expressiveness is traded for testability.
//
// No runtime.GOOS guard accompanies it: the value cannot occur on POSIX, so
// the errno alone decides the fallback, and every other link failure — a path
// that is too long, a target on a network volume — surfaces as the error it is
// instead of a silent copy that hides the problem.
const ErrLinkPrivilegeNotHeld = syscall.Errno(1314)

// CreateSymbolicLink creates one symbolic link. It exists as a test seam: the
// fallback below turns on an errno only Windows produces, so tests replace
// this variable to reach that branch on every platform. Same idiom as
// observeRemoteSource (freshness.go) and syncPromptUnknown (cli/sync.go); it is
// exported because the CLI's tests need to trigger the fallback too.
var CreateSymbolicLink = os.Symlink

// CreateSymlink points dst at src. When the operating system says this process
// may not create a link and src is a directory, dst becomes a managed copy
// instead; anything else is returned as the error it is.
func CreateSymlink(src, dst string, targetIsDirectory bool) error {
	_ = os.RemoveAll(dst)

	err := CreateSymbolicLink(src, dst)
	if err == nil || !targetIsDirectory || !errors.Is(err, ErrLinkPrivilegeNotHeld) {
		return err
	}
	srcAbs := src
	if !filepath.IsAbs(srcAbs) {
		srcAbs = filepath.Join(filepath.Dir(dst), src)
	}
	if fi, statErr := os.Stat(srcAbs); statErr != nil || !fi.IsDir() {
		return err
	}
	return replaceManagedCopy(srcAbs, dst)
}

// writeManagedCopyMarker records what a managed copy was made from: the
// absolute path of the Skill on the first line, and the digest of that Skill's
// content at copy time on the second. The digest is what lets a later apply
// leave an unchanged copy alone instead of rewriting every file. Plain text on
// purpose — JSON would need a version field to say the same thing.
func writeManagedCopyMarker(dir, source, digest string) error {
	return os.WriteFile(filepath.Join(dir, managedCopyMarker), []byte(source+"\n"+digest+"\n"), 0o644)
}

// readManagedCopyMarker splits one managed copy's marker into the Skill it was
// made from and the digest recorded for it. A marker written before digests
// were recorded has no second line and reads as an empty digest — unknown, so
// the copy is rebuilt once and gains one.
func readManagedCopyMarker(copyPath string) (source, digest string, ok bool) {
	data, err := os.ReadFile(filepath.Join(copyPath, managedCopyMarker))
	if err != nil {
		return "", "", false
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	source = strings.TrimSpace(lines[0])
	if len(lines) > 1 {
		digest = strings.TrimSpace(lines[1])
	}
	return source, digest, source != ""
}

// reconcileManagedCopy brings an Availability path that is currently a copy
// back in line with what it stands in for. The link is attempted first, so a
// user who turns Developer Mode on stops having copies rather than keeping
// them forever; only when the privilege is still not held does the copy stay,
// and then it is rebuilt only if the Skill it was made from has changed.
func reconcileManagedCopy(master, linkTarget, dst string) error {
	err := replaceCopyWithLink(linkTarget, dst)
	if err == nil || !errors.Is(err, ErrLinkPrivilegeNotHeld) {
		return err
	}
	return refreshManagedCopy(master, dst)
}

// replaceCopyWithLink creates the link beside the copy and moves it into place
// only once it succeeds. Attempting it at the copy's own path — which
// CreateSymlink would do, clearing the path first — would destroy a working
// copy just to discover that the privilege is still missing. This is the link
// attempt itself, not a proactive privilege probe: it is the same syscall that
// would run if the path were empty.
func replaceCopyWithLink(linkTarget, dst string) error {
	parent := filepath.Dir(dst)
	staging, err := os.MkdirTemp(parent, ".skills-manager-link-")
	if err != nil {
		return err
	}
	if err := os.Remove(staging); err != nil {
		_ = RemoveAll(staging)
		return err
	}
	if err := CreateSymbolicLink(linkTarget, staging); err != nil {
		_ = RemoveAll(staging)
		return err
	}
	defer RemoveAll(staging)
	return installAtomically(staging, dst, "availability link")
}

// installAtomically moves staging onto dst, keeping whatever dst held aside
// until the move has succeeded, so a failure halfway never leaves the path
// empty. what names the thing being installed, for the one error a failed
// rollback has to report.
func installAtomically(staging, dst, what string) error {
	parent := filepath.Dir(dst)
	backup := ""
	if _, err := os.Lstat(dst); err == nil {
		backup, err = os.MkdirTemp(parent, ".skills-manager-backup-")
		if err != nil {
			return err
		}
		if err := os.Remove(backup); err != nil {
			_ = RemoveAll(backup)
			return err
		}
		if err := os.Rename(dst, backup); err != nil {
			return err
		}
	}
	if err := os.Rename(staging, dst); err != nil {
		if backup != "" {
			if rollbackErr := os.Rename(backup, dst); rollbackErr != nil {
				return fmt.Errorf("failed to install %s: %w; rollback failed: %v; previous content remains at %s", what, err, rollbackErr, backup)
			}
		}
		return err
	}
	// The install itself has succeeded by here, so a backup that will not go
	// away is not work that could not be completed (ADR-0002) and must not
	// become one. It is a dot-prefixed temporary the next run ignores.
	if backup != "" {
		_ = RemoveAll(backup)
	}
	return nil
}

// refreshManagedCopy rebuilds an existing managed copy only when the Skill it
// was made from has changed, so a routine Sync does not rewrite every file in
// every Agent directory. Only the source Skill's digest is compared: a copy
// under an Agent directory is a derived artifact, and treating a hand-edited
// one as Drift would give Windows a state that exists on no other platform.
// User edits are protected where they are declared, in the master skills
// directory, which already has a recorded baseline.
func refreshManagedCopy(src, dst string) error {
	if _, recorded, ok := readManagedCopyMarker(dst); ok && recorded != "" {
		if current, err := DigestSkillTree(skillContentRoot(src)); err == nil && current == recorded {
			return nil
		}
	}
	return replaceManagedCopy(src, dst)
}

// skillContentRoot is where a Skill's content actually lives. A Skill declared
// from a local Source is a symlink on the skills directory, and walking that
// path as given reproduces the link instead of reading through it: the copy
// would come out as a second link, its digest would be the digest of an empty
// tree, and the marker would be written into the user's own Source directory.
// Callers keep addressing the Skill by its master path; only the reading of
// its bytes follows the link.
func skillContentRoot(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func replaceManagedCopy(src, dst string) error {
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".skills-manager-copy-")
	if err != nil {
		return err
	}
	defer RemoveAll(staging)
	content := skillContentRoot(src)
	if err := CopySkillFolder(content, staging); err != nil {
		return err
	}
	// The marker names the master Skill, not where its bytes came from: that
	// is what isManagedSkillCopy matches against to recognize its own work.
	absSource, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	digest, err := DigestSkillTree(content)
	if err != nil {
		return err
	}
	if err := writeManagedCopyMarker(staging, filepath.Clean(absSource), digest); err != nil {
		return err
	}
	return installAtomically(staging, dst, "managed copy")
}

func ensureAgentSymlink(
	skillName string,
	agentName string,
	skillsDir string,
) (bool, error) {
	if skillsDir == "" {
		skillsDir = models.DefaultSkillsDir()
	}
	normAgent := models.NormalizeAgentName(agentName)
	knownAgents := models.GetAgentsForSkillsDir(skillsDir)
	agentDir, ok := knownAgents[normAgent]
	if !ok {
		return false, nil
	}

	masterSkillPath := filepath.Join(skillsDir, skillName)
	if _, err := os.Stat(masterSkillPath); err != nil {
		// Also check if master is a symlink
		if _, lErr := os.Lstat(masterSkillPath); lErr != nil {
			return false, nil
		}
	}

	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return false, fmt.Errorf("failed to create agent dir %s: %w", agentDir, err)
	}

	agentLink := filepath.Join(agentDir, skillName)

	// Determine relative symlink target
	relTarget, err := filepath.Rel(agentDir, masterSkillPath)
	if err != nil {
		relTarget = masterSkillPath
	}

	if fi, err := os.Lstat(agentLink); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(agentLink)
			live := err == nil && (target == relTarget || filepath.Clean(target) == filepath.Clean(masterSkillPath))
			if live {
				if _, statErr := os.Stat(agentLink); statErr == nil {
					return true, nil
				}
			}
			if isManagedSkillLink(agentLink, skillName, skillsDir) {
				if err := os.Remove(agentLink); err != nil {
					return false, err
				}
			} else if isManagedSkillCopy(agentLink, skillName, skillsDir) {
				if err := reconcileManagedCopy(masterSkillPath, relTarget, agentLink); err != nil {
					return false, err
				}
				return true, nil
			} else {
				return false, fmt.Errorf("agent path already exists and is not a managed link: %s", agentLink)
			}
		} else if isManagedSkillCopy(agentLink, skillName, skillsDir) {
			if err := reconcileManagedCopy(masterSkillPath, relTarget, agentLink); err != nil {
				return false, err
			}
			return true, nil
		} else {
			return false, fmt.Errorf("agent path already exists and is not a managed link: %s", agentLink)
		}
	}

	if err := CreateSymlink(relTarget, agentLink, true); err != nil {
		return false, err
	}
	return true, nil
}

// removeManagedSkillPath reports both whether path was Availability this tool
// created and how removing it went, for the caller that has to tell a path it
// left alone from one it could not remove. One place decides which mechanism a
// managed path used, so a third would not have to be taught to two callers.
func removeManagedSkillPath(path, skillName, skillsDir string) (managed bool, err error) {
	if isManagedSkillLink(path, skillName, skillsDir) {
		return true, os.Remove(path)
	}
	// A copy is a directory, so removing it takes more than os.Remove —
	// otherwise removing a Skill on Windows leaves its Availability behind.
	if isManagedSkillCopy(path, skillName, skillsDir) {
		return true, RemoveAll(path)
	}
	return false, nil
}

func isManagedSkillPath(path, skillName, skillsDir string) bool {
	return isManagedSkillLink(path, skillName, skillsDir) || isManagedSkillCopy(path, skillName, skillsDir)
}

func isManagedSkillCopy(path, skillName, skillsDir string) bool {
	fi, err := os.Lstat(path)
	if err != nil || !fi.IsDir() {
		return false
	}
	marked, _, ok := readManagedCopyMarker(path)
	if !ok {
		return false
	}
	base := skillsDir
	if base == "" {
		base = models.DefaultSkillsDir()
	}
	expected, err1 := filepath.Abs(filepath.Join(base, skillName))
	source, err2 := filepath.Abs(marked)
	return err1 == nil && err2 == nil && filepath.Clean(source) == filepath.Clean(expected)
}

func isManagedSkillLink(linkPath string, skillName string, skillsDir string) bool {
	fi, err := os.Lstat(linkPath)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return false
	}
	target, err := os.Readlink(linkPath)
	if err != nil {
		return false
	}
	base := skillsDir
	if base == "" {
		base = models.DefaultSkillsDir()
	}
	master := filepath.Join(base, skillName)
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(linkPath), target)
	}
	absTarget, err1 := filepath.Abs(filepath.Clean(target))
	absMaster, err2 := filepath.Abs(filepath.Clean(master))
	if err1 != nil || err2 != nil {
		return false
	}
	return absTarget == absMaster
}
