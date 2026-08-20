package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

func CreateSymlink(src, dst string, targetIsDirectory bool) error {
	_ = os.RemoveAll(dst)

	err := os.Symlink(src, dst)
	if err != nil && runtime.GOOS == "windows" && targetIsDirectory {
		// Fallback for Windows if Developer Mode is off: copy the directory
		srcAbs := src
		if !filepath.IsAbs(srcAbs) {
			srcAbs = filepath.Join(filepath.Dir(dst), src)
		}
		if fi, statErr := os.Stat(srcAbs); statErr == nil && fi.IsDir() {
			return CopySkillFolder(srcAbs, dst)
		}
	}
	return err
}

func IsSkillExcludedForAgent(
	skillName string,
	source string,
	agent string,
	agentExclusions map[string][]string,
) bool {
	normAgent := models.NormalizeAgentName(agent)
	for k, patterns := range agentExclusions {
		if models.NormalizeAgentName(k) == normAgent {
			for _, pat := range patterns {
				pLow := strings.ToLower(strings.TrimSpace(pat))
				if pLow == strings.ToLower(skillName) || pLow == strings.ToLower(source) {
					return true
				}
			}
		}
	}
	return false
}

func GetTargetAgentsForSkill(
	skillName string,
	source string,
	cfg *config.Config,
	skillsDir ...string,
) []string {
	defaultAgents := cfg.Settings.DefaultAgents
	if len(defaultAgents) == 0 {
		defaultAgents = []string{"claude"}
	}

	excludeSet := make(map[string]struct{})
	for _, a := range cfg.Settings.ExcludeAgents {
		excludeSet[models.NormalizeAgentName(a)] = struct{}{}
	}

	var baseSkills string
	if len(skillsDir) > 0 {
		baseSkills = skillsDir[0]
	}
	knownAgents := models.GetAgentsForSkillsDir(baseSkills)
	activeAgents := make([]string, 0)

	for _, a := range defaultAgents {
		norm := models.NormalizeAgentName(a)
		if _, excluded := excludeSet[norm]; excluded {
			continue
		}
		if IsSkillExcludedForAgent(skillName, source, norm, cfg.Settings.AgentExclusions) {
			continue
		}
		if _, isKnown := knownAgents[norm]; isKnown {
			activeAgents = append(activeAgents, norm)
		}
	}

	return activeAgents
}

func LinkSkillToTargetAgents(
	skillName string,
	source string,
	cfg *config.Config,
	skillsDir string,
) ([]string, error) {
	linked := make([]string, 0)
	for _, agent := range GetTargetAgentsForSkill(skillName, source, cfg, skillsDir) {
		created, err := EnsureAgentSymlink(skillName, agent, skillsDir)
		if err != nil {
			return linked, err
		}
		if created {
			linked = append(linked, agent)
		}
	}
	return linked, nil
}

func EnsureAgentSymlink(
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

	// Check if already correctly linked
	if fi, err := os.Lstat(agentLink); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(agentLink)
			if err == nil && (target == relTarget || filepath.Clean(target) == filepath.Clean(masterSkillPath)) {
				return true, nil
			}
		}
		_ = os.RemoveAll(agentLink)
	}

	if err := CreateSymlink(relTarget, agentLink, true); err != nil {
		return false, err
	}
	return true, nil
}

func RemoveAgentSymlinks(skillName string, skillsDir ...string) []string {
	removed := make([]string, 0)
	var dir string
	if len(skillsDir) > 0 {
		dir = skillsDir[0]
	}
	knownAgents := models.GetAgentsForSkillsDir(dir)

	for agentName, agentDir := range knownAgents {
		linkPath := filepath.Join(agentDir, skillName)
		if _, err := os.Lstat(linkPath); err == nil {
			_ = os.RemoveAll(linkPath)
			removed = append(removed, agentName)
		}
	}
	return removed
}
