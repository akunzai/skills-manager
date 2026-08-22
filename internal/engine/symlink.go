package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

const managedCopyMarker = ".skills-manager-copy"

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
			return replaceManagedCopy(srcAbs, dst)
		}
	}
	return err
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
	if err := CopySkillFolder(src, staging); err != nil {
		return err
	}
	absSource, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(staging, managedCopyMarker), []byte(filepath.Clean(absSource)+"\n"), 0o644); err != nil {
		return err
	}

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
				return fmt.Errorf("failed to install managed copy: %w; rollback failed: %v; previous copy remains at %s", err, rollbackErr, backup)
			}
		}
		return err
	}
	if backup != "" {
		if err := RemoveAll(backup); err != nil {
			return fmt.Errorf("managed copy refreshed but failed to remove backup %s: %w", backup, err)
		}
	}
	return nil
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
	override := cfg.Settings.Availability[skillName]
	includeSet := agentSet(override.Include)
	perSkillExcludeSet := agentSet(override.Exclude)
	candidates := append([]string{}, defaultAgents...)
	candidates = append(candidates, override.Include...)
	seen := make(map[string]struct{}, len(candidates))
	activeAgents := make([]string, 0, len(candidates))

	for _, a := range candidates {
		norm := models.NormalizeAgentName(a)
		if _, duplicate := seen[norm]; duplicate {
			continue
		}
		seen[norm] = struct{}{}
		if _, excluded := perSkillExcludeSet[norm]; excluded {
			continue
		}
		if _, included := includeSet[norm]; !included {
			if _, excluded := excludeSet[norm]; excluded {
				continue
			}
			if IsSkillExcludedForAgent(skillName, source, norm, cfg.Settings.AgentExclusions) {
				continue
			}
		}
		if _, isKnown := knownAgents[norm]; isKnown {
			activeAgents = append(activeAgents, norm)
		}
	}

	return activeAgents
}

func agentSet(agents []string) map[string]struct{} {
	set := make(map[string]struct{}, len(agents))
	for _, agent := range agents {
		set[models.NormalizeAgentName(agent)] = struct{}{}
	}
	return set
}

func ReconcileAgentSymlinks(skillName, source string, cfg *config.Config, skillsDir string) error {
	desired := agentSet(GetTargetAgentsForSkill(skillName, source, cfg, skillsDir))
	knownAgents := models.GetAgentsForSkillsDir(skillsDir)
	for agent := range desired {
		agentDir, ok := knownAgents[agent]
		if !ok {
			continue
		}
		linkPath := filepath.Join(agentDir, skillName)
		_, err := os.Lstat(linkPath)
		if err == nil && !IsManagedSkillLink(linkPath, skillName, skillsDir) && !IsManagedSkillCopy(linkPath, skillName, skillsDir) {
			return fmt.Errorf("agent path already exists and is not managed by skills: %s", linkPath)
		}
	}
	for agent, agentDir := range knownAgents {
		if _, shouldLink := desired[agent]; shouldLink {
			linkPath := filepath.Join(agentDir, skillName)
			if IsManagedSkillCopy(linkPath, skillName, skillsDir) {
				if err := replaceManagedCopy(filepath.Join(skillsDir, skillName), linkPath); err != nil {
					return err
				}
				continue
			}
			if _, err := EnsureAgentSymlink(skillName, agent, skillsDir); err != nil {
				return err
			}
			continue
		}
		RemoveManagedSkillPath(filepath.Join(agentDir, skillName), skillName, skillsDir)
	}
	return nil
}

func AgentLinkDrift(skillName, source string, cfg *config.Config, skillsDir string) (missing, unexpected []string) {
	desired := agentSet(GetTargetAgentsForSkill(skillName, source, cfg, skillsDir))
	for agent, agentDir := range models.GetAgentsForSkillsDir(skillsDir) {
		linkPath := filepath.Join(agentDir, skillName)
		_, exists := desired[agent]
		if exists {
			_, err := os.Lstat(linkPath)
			if os.IsNotExist(err) || (err == nil && !IsManagedSkillLink(linkPath, skillName, skillsDir) && !IsManagedSkillCopy(linkPath, skillName, skillsDir)) {
				missing = append(missing, agent)
			}
			continue
		}
		if IsManagedSkillLink(linkPath, skillName, skillsDir) || IsManagedSkillCopy(linkPath, skillName, skillsDir) {
			unexpected = append(unexpected, agent)
		}
	}
	sort.Strings(missing)
	sort.Strings(unexpected)
	return missing, unexpected
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
		return false, fmt.Errorf("agent path already exists and is not a managed link: %s", agentLink)
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
		if RemoveManagedSkillPath(linkPath, skillName, dir) {
			removed = append(removed, agentName)
		}
	}

	// Universal agents read the master skills directory directly, so nothing
	// here is created by us — but older versions and setup scripts did populate
	// these directories, and a link left pointing at a removed skill dangles
	// forever otherwise.
	for agentName, agentDir := range models.GetUniversalAgentSkillDirs(dir) {
		if _, taken := knownAgents[agentName]; taken {
			continue
		}
		if RemoveManagedSkillLink(filepath.Join(agentDir, skillName), skillName, dir) {
			removed = append(removed, agentName)
		}
	}
	return removed
}

func RemoveManagedSkillPath(path, skillName, skillsDir string) bool {
	if IsManagedSkillLink(path, skillName, skillsDir) {
		return os.Remove(path) == nil
	}
	if IsManagedSkillCopy(path, skillName, skillsDir) {
		return os.RemoveAll(path) == nil
	}
	return false
}

func IsManagedSkillCopy(path, skillName, skillsDir string) bool {
	fi, err := os.Lstat(path)
	if err != nil || !fi.IsDir() {
		return false
	}
	data, err := os.ReadFile(filepath.Join(path, managedCopyMarker))
	if err != nil {
		return false
	}
	base := skillsDir
	if base == "" {
		base = models.DefaultSkillsDir()
	}
	expected, err1 := filepath.Abs(filepath.Join(base, skillName))
	marked, err2 := filepath.Abs(strings.TrimSpace(string(data)))
	return err1 == nil && err2 == nil && filepath.Clean(marked) == filepath.Clean(expected)
}

// RemoveManagedSkillLink deletes linkPath only when it is a symlink that points
// at skillName inside the master skills directory. Anything else — a real
// directory, a link somewhere else entirely — is left untouched, so an
// imprecise agent path can never cost the user unrelated files.
func RemoveManagedSkillLink(linkPath string, skillName string, skillsDir string) bool {
	if !IsManagedSkillLink(linkPath, skillName, skillsDir) {
		return false
	}
	return os.Remove(linkPath) == nil
}

// IsManagedSkillLink reports whether linkPath is a symlink this tool would have
// created for skillName, resolved or dangling.
func IsManagedSkillLink(linkPath string, skillName string, skillsDir string) bool {
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
