package engine

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/akunzai/skills-manager/internal/models"
)

// AgentDir is a known harness skills directory.
type AgentDir struct {
	Name string
	Dir  string
}

// AgentLinkManager handles agent skills directories, symlink creation,
// health inspection, managed link removal, and boundary-safe parent directory pruning.
type AgentLinkManager struct {
	skillsDir string
	stopAt    string
}

func NewAgentLinkManager(skillsDir string) AgentLinkManager {
	if skillsDir == "" {
		skillsDir = models.DefaultSkillsDir()
	}
	return AgentLinkManager{
		skillsDir: skillsDir,
		stopAt:    models.ScopeRoot(skillsDir),
	}
}

func (m AgentLinkManager) EnsureLink(skillName, agentName string) (bool, error) {
	return EnsureAgentSymlink(skillName, agentName, m.skillsDir)
}

func (m AgentLinkManager) RemoveLinks(skillName string) []string {
	return RemoveAgentSymlinks(skillName, m.skillsDir)
}

func (m AgentLinkManager) DiagnoseHealth(agentDir string) (broken, unmanagedBroken, physical []string) {
	return DiagnoseAgentDirHealth(agentDir, m.skillsDir)
}

func (m AgentLinkManager) FindStaleLinks(agentDir string) []string {
	return FindStaleManagedLinks(agentDir, m.skillsDir)
}

func (m AgentLinkManager) RemoveEmptyDir(agentDir string) error {
	return RemoveEmptyAgentDir(agentDir, m.stopAt)
}

func (m AgentLinkManager) PruneParents(dir string) error {
	return pruneEmptyParents(dir, m.stopAt)
}

func (m AgentLinkManager) IsManagedLink(linkPath, skillName string) bool {
	return IsManagedSkillLink(linkPath, skillName, m.skillsDir)
}

func (m AgentLinkManager) IsManagedCopy(path, skillName string) bool {
	return IsManagedSkillCopy(path, skillName, m.skillsDir)
}

func (m AgentLinkManager) RemoveManagedPath(path, skillName string) bool {
	return RemoveManagedSkillPath(path, skillName, m.skillsDir)
}

// DiagnoseAgentDirHealth classifies every entry in a configured agent's
// skills directory: broken is a managed symlink whose target has gone
// missing, unmanagedBroken is a dangling symlink this tool never created,
// and physical is a real directory sitting where a managed symlink is
// expected (and isn't a copy this tool made, e.g. the Windows fallback).
// A missing agentDir is not itself unhealthy: it reports no findings.
func DiagnoseAgentDirHealth(agentDir, skillsDir string) (broken, unmanagedBroken, physical []string) {
	entries, err := os.ReadDir(agentDir)
	if err != nil {
		return nil, nil, nil
	}

	for _, entry := range entries {
		name := entry.Name()
		fullPath := filepath.Join(agentDir, name)

		fi, err := os.Lstat(fullPath)
		if err != nil {
			continue
		}

		if fi.Mode()&os.ModeSymlink != 0 {
			if _, err := os.Stat(fullPath); err != nil {
				if IsManagedSkillLink(fullPath, name, skillsDir) {
					broken = append(broken, name)
				} else {
					unmanagedBroken = append(unmanagedBroken, name)
				}
			}
		} else if fi.IsDir() && !strings.HasPrefix(name, ".") && !IsManagedSkillCopy(fullPath, name, skillsDir) {
			physical = append(physical, name)
		}
	}

	return broken, unmanagedBroken, physical
}

// FindStaleManagedLinks returns managed symlinks in a universal agent's
// skills directory whose target skill no longer exists. Universal agents
// read the master skills directory directly, so skills-manager never
// creates these paths, but earlier versions and setup scripts did, and a
// link left behind after a skill is removed dangles forever otherwise. A
// missing agentDir reports no findings.
func FindStaleManagedLinks(agentDir, skillsDir string) []string {
	entries, err := os.ReadDir(agentDir)
	if err != nil {
		return nil
	}

	var stale []string
	for _, entry := range entries {
		linkPath := filepath.Join(agentDir, entry.Name())
		if !IsManagedSkillLink(linkPath, entry.Name(), skillsDir) {
			continue
		}
		if _, err := os.Stat(linkPath); err != nil {
			stale = append(stale, entry.Name())
		}
	}
	return stale
}

// LeftoverEmptyAgentDirs returns known agent skills dirs that exist, are
// effectively empty, and are not in the configured set.
func LeftoverEmptyAgentDirs(known, configured map[string]string) []AgentDir {
	var leftover []AgentDir
	for name, dir := range known {
		if _, ok := configured[name]; ok {
			continue
		}
		empty, err := isDirEffectivelyEmpty(dir)
		if err != nil || !empty {
			continue
		}
		leftover = append(leftover, AgentDir{Name: name, Dir: dir})
	}
	slices.SortFunc(leftover, func(a, b AgentDir) int { return cmp.Compare(a.Name, b.Name) })
	return leftover
}

// RemoveEmptyAgentDir deletes an empty agent skills directory and prunes
// empty parents. Pruning never escapes stopAt (when non-empty), and always
// stops at $HOME, XDG_CONFIG_HOME, and ~/.local.
func RemoveEmptyAgentDir(agentDir string, stopAt string) error {
	if agentDir == "" {
		return nil
	}
	fi, err := os.Lstat(agentDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return nil
	}
	empty, err := isDirEffectivelyEmpty(agentDir)
	if err != nil {
		return err
	}
	if !empty {
		return nil
	}
	if err := os.RemoveAll(agentDir); err != nil {
		return err
	}
	return pruneEmptyParents(filepath.Dir(agentDir), stopAt)
}

// isDirEffectivelyEmpty reports whether an agent skills directory holds no
// skills. Dot entries (e.g. .DS_Store) do not count as skills.
func isDirEffectivelyEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			return false, nil
		}
	}
	return true, nil
}

// isDirCompletelyEmpty reports whether dir holds no entries at all. Parent
// pruning uses this stricter test so that hidden entries such as .git,
// .agents, or .claude keep a directory alive.
func isDirCompletelyEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

func pruneEmptyParents(dir string, stopAt string) error {
	home := filepath.Clean(models.UserHomeDir())
	xdgConfig := filepath.Clean(models.ResolveEnvPath("XDG_CONFIG_HOME", "~/.config"))
	localHome := filepath.Clean(filepath.Join(home, ".local"))

	boundary := ""
	if stopAt != "" {
		boundary = filepath.Clean(stopAt)
	}

	for dir != "" {
		clean := filepath.Clean(dir)
		if clean == home || clean == xdgConfig || clean == localHome || clean == boundary {
			return nil
		}
		if clean == "/" || clean == "." || filepath.Dir(clean) == clean {
			return nil
		}
		// With an explicit boundary (project scope) it is the only thing that
		// keeps pruning inside the project; without one, stay under $HOME.
		inScope := pathIsUnder(clean, home) || pathIsUnder(clean, xdgConfig)
		if boundary != "" {
			inScope = pathIsUnder(clean, boundary)
		}
		if !inScope {
			return nil
		}
		empty, err := isDirCompletelyEmpty(clean)
		if err != nil || !empty {
			return err
		}
		if err := os.RemoveAll(clean); err != nil {
			return err
		}
		dir = filepath.Dir(clean)
	}
	return nil
}

func pathIsUnder(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
