package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// DesiredAgents is the Agents declared Availability selects for one Skill:
// defaultAgents, plus Include, minus Exclude. Automatically available Agents are
// not in this set.
func DesiredAgents(skillName string, cfg *config.Config, skillsDir string) []string {
	defaultAgents := cfg.Settings.DefaultAgents
	if len(defaultAgents) == 0 {
		defaultAgents = []string{"claude"}
	}

	knownAgents := models.GetAgentsForSkillsDir(skillsDir)
	override := cfg.Settings.Availability[skillName]
	excludeSet := agentSet(override.Exclude)
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
		if _, excluded := excludeSet[norm]; excluded {
			continue
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

func isManagedAvailabilityPath(path, skillName, skillsDir string) bool {
	return IsManagedSkillLink(path, skillName, skillsDir) || IsManagedSkillCopy(path, skillName, skillsDir)
}

// availabilityState is desired Availability vs disk for one Skill.
type availabilityState struct {
	skillName string
	skillsDir string
	desired   map[string]struct{}
	known     map[string]string
}

func availabilityFor(skillName string, cfg *config.Config, skillsDir string) availabilityState {
	return availabilityState{
		skillName: skillName,
		skillsDir: skillsDir,
		desired:   agentSet(DesiredAgents(skillName, cfg, skillsDir)),
		known:     models.GetAgentsForSkillsDir(skillsDir),
	}
}

func (s availabilityState) drift() (missing, unexpected []string) {
	for agent, agentDir := range s.known {
		linkPath := filepath.Join(agentDir, s.skillName)
		_, want := s.desired[agent]
		if want {
			_, err := os.Lstat(linkPath)
			if os.IsNotExist(err) || (err == nil && !isManagedAvailabilityPath(linkPath, s.skillName, s.skillsDir)) {
				missing = append(missing, agent)
			}
			continue
		}
		if isManagedAvailabilityPath(linkPath, s.skillName, s.skillsDir) {
			unexpected = append(unexpected, agent)
		}
	}
	sort.Strings(missing)
	sort.Strings(unexpected)
	return missing, unexpected
}

func (s availabilityState) apply() error {
	for agent := range s.desired {
		agentDir, ok := s.known[agent]
		if !ok {
			continue
		}
		linkPath := filepath.Join(agentDir, s.skillName)
		_, err := os.Lstat(linkPath)
		if err == nil && !isManagedAvailabilityPath(linkPath, s.skillName, s.skillsDir) {
			return fmt.Errorf("agent path already exists and is not managed by skills: %s", linkPath)
		}
	}
	for agent, agentDir := range s.known {
		linkPath := filepath.Join(agentDir, s.skillName)
		if _, shouldLink := s.desired[agent]; shouldLink {
			if IsManagedSkillCopy(linkPath, s.skillName, s.skillsDir) {
				if err := replaceManagedCopy(filepath.Join(s.skillsDir, s.skillName), linkPath); err != nil {
					return err
				}
				continue
			}
			if _, err := EnsureAgentSymlink(s.skillName, agent, s.skillsDir); err != nil {
				return err
			}
			continue
		}
		if isManagedAvailabilityPath(linkPath, s.skillName, s.skillsDir) && !RemoveManagedSkillPath(linkPath, s.skillName, s.skillsDir) {
			return fmt.Errorf("failed to remove managed availability path: %s", linkPath)
		}
	}
	return nil
}

// ApplyAvailability writes declared Availability onto disk for one Skill.
func ApplyAvailability(skillName string, cfg *config.Config, skillsDir string) error {
	return availabilityFor(skillName, cfg, skillsDir).apply()
}

// AvailabilityDrift is declared Availability vs managed disk links for one Skill.
func AvailabilityDrift(skillName string, cfg *config.Config, skillsDir string) (missing, unexpected []string) {
	return availabilityFor(skillName, cfg, skillsDir).drift()
}

// applyDeclaredAvailability applies Availability, or records Drift when dry-run.
func applyDeclaredAvailability(name, source string, cfg *config.Config, skillsDir string, dryRun bool, report *SyncReport) error {
	if dryRun {
		report.driftEvent(name, source, cfg, skillsDir)
		return nil
	}
	if err := ApplyAvailability(name, cfg, skillsDir); err != nil {
		return fmt.Errorf("failed to apply availability for %s: %w", name, err)
	}
	return nil
}
