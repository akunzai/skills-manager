package engine

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/akunzai/skills-manager/internal/models"
)

// AgentDir is a known harness skills directory.
type AgentDir struct {
	Name   string
	Dir    string
	Repair ItemRepair
}

// RemoveEmptyAgentDir deletes an empty agent skills directory and prunes
// empty parents. Pruning never escapes stopAt (when non-empty), and always
// stops at $HOME, XDG_CONFIG_HOME, and ~/.local.
func removeEmptyAgentDir(agentDir string, stopAt string) error {
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
