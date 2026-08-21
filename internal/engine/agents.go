package engine

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// AgentDir is a known harness skills directory.
type AgentDir struct {
	Name string
	Dir  string
}

// ConfiguredKnownAgents returns non-universal agent dirs selected by
// settings.defaultAgents after applying excludeAgents.
func ConfiguredKnownAgents(cfg *config.Config, skillsDir string) map[string]string {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	defaultAgents := cfg.Settings.DefaultAgents
	if len(defaultAgents) == 0 {
		defaultAgents = []string{"claude"}
	}

	excludeSet := make(map[string]struct{})
	for _, a := range cfg.Settings.ExcludeAgents {
		excludeSet[models.NormalizeAgentName(a)] = struct{}{}
	}

	known := models.GetAgentsForSkillsDir(skillsDir)
	out := make(map[string]string)
	for _, a := range defaultAgents {
		norm := models.NormalizeAgentName(a)
		if _, excluded := excludeSet[norm]; excluded {
			continue
		}
		if dir, ok := known[norm]; ok {
			out[norm] = dir
		}
	}
	return out
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
	sort.Slice(leftover, func(i, j int) bool { return leftover[i].Name < leftover[j].Name })
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
