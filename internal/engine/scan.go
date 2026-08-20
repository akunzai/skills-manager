package engine

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

func ScanAllSkills(cfg *config.Config, skillsDir string) []models.SkillItem {
	baseSkills := skillsDir
	if baseSkills == "" {
		baseSkills = models.DefaultSkillsDir()
	}

	items := make(map[string]*models.SkillItem)

	// 1. Configured Remote Skills
	for sourceKey, repoInfo := range cfg.Remote {
		for name, subpath := range repoInfo.Skills {
			repoType := repoInfo.Type
			if repoType == "" {
				repoType = "github"
			}
			items[name] = &models.SkillItem{
				Name:         name,
				SourceType:   repoType,
				Source:       sourceKey,
				Subpath:      subpath,
				LinkedAgents: make([]string, 0),
			}
		}
	}

	// 2. Configured Local Skills
	for name, localInfo := range cfg.Local {
		src := localInfo.Source
		if src == "" {
			src = localInfo.Command
		}
		if src == "" {
			src = "local"
		}
		items[name] = &models.SkillItem{
			Name:         name,
			SourceType:   "local_" + localInfo.Type,
			Source:       src,
			Description:  localInfo.Description,
			LinkedAgents: make([]string, 0),
		}
	}

	// 3. Physical Directory State in skillsDir
	if entries, err := os.ReadDir(baseSkills); err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}

			fullPath := filepath.Join(baseSkills, name)
			item, exists := items[name]
			if !exists {
				sourceType := "untracked"
				source := "local"
				if info, err := os.Lstat(fullPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
					sourceType = "symlink"
					if linkTarget, err := os.Readlink(fullPath); err == nil {
						source = linkTarget
					}
				}
				item = &models.SkillItem{
					Name:         name,
					SourceType:   sourceType,
					Source:       source,
					LinkedAgents: make([]string, 0),
				}
				items[name] = item
			}

			item.InstalledPath = fullPath
			item.IsInstalled = true
			skillMd := filepath.Join(fullPath, "SKILL.md")
			if _, err := os.Stat(skillMd); err == nil {
				item.IsValidSkill = true
			}
		}
	}

	// 4. Check Agent Link States
	knownAgents := models.GetAgentsForSkillsDir(baseSkills)
	for name, item := range items {
		linked := make([]string, 0)
		for agentName, agentDir := range knownAgents {
			linkPath := filepath.Join(agentDir, name)
			if _, err := os.Lstat(linkPath); err == nil {
				linked = append(linked, agentName)
			}
		}
		sort.Strings(linked)
		item.LinkedAgents = linked
	}

	// Convert map to slice and sort
	result := make([]models.SkillItem, 0, len(items))
	for _, item := range items {
		result = append(result, *item)
	}

	sort.Slice(result, func(i, j int) bool {
		if strings.ToLower(result[i].Source) != strings.ToLower(result[j].Source) {
			return strings.ToLower(result[i].Source) < strings.ToLower(result[j].Source)
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})

	return result
}
