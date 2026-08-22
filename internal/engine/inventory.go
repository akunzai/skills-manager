package engine

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// Inventory is declared Skills for one Scope plus what is on its skills
// directory, classified as missing, untracked, or invalid. Configured entries
// carry declared Availability; untracked entries have none.
func Inventory(cfg *config.Config, skillsDir string) ([]models.SkillItem, error) {
	baseSkills := skillsDir
	if baseSkills == "" {
		baseSkills = models.DefaultSkillsDir()
	}

	items := make(map[string]*models.SkillItem)

	for sourceKey, repoInfo := range cfg.Remote {
		for name, subpath := range repoInfo.Skills {
			repoType := repoInfo.Type
			if repoType == "" {
				repoType = "github"
			}
			items[name] = &models.SkillItem{
				Name:       name,
				SourceType: repoType,
				Source:     sourceKey,
				Subpath:    subpath,
				Agents:     DesiredAgents(name, cfg, baseSkills),
			}
		}
	}

	for name, localInfo := range cfg.Local {
		src := localInfo.Source
		if src == "" {
			src = localInfo.Command
		}
		if src == "" {
			src = "local"
		}
		items[name] = &models.SkillItem{
			Name:        name,
			SourceType:  "local_" + localInfo.Type,
			Source:      src,
			Description: localInfo.Description,
			Agents:      DesiredAgents(name, cfg, baseSkills),
		}
	}

	entries, err := os.ReadDir(baseSkills)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
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
					Name:       name,
					SourceType: sourceType,
					Source:     source,
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

	result := make([]models.SkillItem, 0, len(items))
	for _, item := range items {
		if item.Agents == nil {
			item.Agents = []string{}
		}
		result = append(result, *item)
	}

	sort.Slice(result, func(i, j int) bool {
		if strings.ToLower(result[i].Source) != strings.ToLower(result[j].Source) {
			return strings.ToLower(result[i].Source) < strings.ToLower(result[j].Source)
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})

	return result, nil
}

func isUntracked(item models.SkillItem) bool {
	return item.SourceType == "untracked" || item.SourceType == "symlink"
}
