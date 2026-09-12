package engine

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// Inventory is declared Skills for one Scope plus what is on its skills
// directory, classified as missing, untracked, invalid, stub, or illegal-local.
type Inventory struct {
	missing        []string
	untracked      []string
	untrackedLinks []string
	invalid        []InvalidSkill
	stubs          []string
	illegalLocal   []IllegalLocalSource
	present        []presentSkill
	items          []models.SkillItem
}

type presentSkill struct {
	Name       string
	SourceType string
	Source     string
}

func (inv Inventory) Missing() []string { return slices.Clone(inv.missing) }

func (inv Inventory) Untracked() []string { return slices.Clone(inv.untracked) }

func (inv Inventory) UntrackedLinks() []string { return slices.Clone(inv.untrackedLinks) }

func (inv Inventory) Invalid() []InvalidSkill { return slices.Clone(inv.invalid) }

func (inv Inventory) Stubs() []string { return slices.Clone(inv.stubs) }

func (inv Inventory) IllegalLocal() []IllegalLocalSource { return slices.Clone(inv.illegalLocal) }

// SkillItems projects classified occupancy into the JSON DTO. Callers that
// decide from occupancy use the typed queries instead of SourceType.
func (inv Inventory) SkillItems() []models.SkillItem { return slices.Clone(inv.items) }

func (inv Inventory) declaredPresent() []presentSkill { return inv.present }

// LoadInventory observes Config and the skills directory once and classifies
// occupancy. Go does not allow a function named Inventory beside the type.
func LoadInventory(cfg *config.Config, skillsDir string) (Inventory, error) {
	baseSkills := skillsDir
	if baseSkills == "" {
		baseSkills = models.DefaultSkillsDir()
	}
	availability := NewAvailability(cfg, baseSkills)

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
				Agents:     availability.ManagedAgents(name),
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
			Agents:      availability.ManagedAgents(name),
		}
	}

	if reason := unusableDirectory(baseSkills); reason != "" {
		return Inventory{}, fmt.Errorf("skills directory %s: %s", baseSkills, reason)
	}
	kind := make(map[string]os.FileMode)
	entries, err := os.ReadDir(baseSkills)
	if err != nil && !os.IsNotExist(err) {
		return Inventory{}, err
	}
	if err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}

			fullPath := filepath.Join(baseSkills, name)
			info, lerr := os.Lstat(fullPath)
			if lerr == nil {
				kind[name] = info.Mode()
			}
			item, exists := items[name]
			if !exists {
				sourceType := "untracked"
				source := "local"
				if info != nil && info.Mode()&os.ModeSymlink != 0 {
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

	slices.SortFunc(result, func(a, b models.SkillItem) int {
		return cmp.Or(
			cmp.Compare(strings.ToLower(a.Source), strings.ToLower(b.Source)),
			cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
		)
	})

	inv := Inventory{items: result}
	for _, item := range result {
		mode := kind[item.Name]
		switch {
		case !item.IsInstalled:
			inv.missing = append(inv.missing, item.Name)
		case item.SourceType == "symlink":
			inv.untrackedLinks = append(inv.untrackedLinks, item.Name)
		case item.SourceType == "untracked":
			inv.untracked = append(inv.untracked, item.Name)
		case item.SourceType == "local_symlink" && models.LocalSourceInsideSkillsDir(models.ResolveLocalSourcePath(item.Source, baseSkills), baseSkills) && mode&os.ModeSymlink == 0 && mode != 0:
			inv.illegalLocal = append(inv.illegalLocal, IllegalLocalSource{Name: item.Name, Source: item.Source})
		case item.SourceType == "local_symlink" && mode.IsRegular():
			inv.stubs = append(inv.stubs, item.Name)
			inv.present = append(inv.present, presentSkill{Name: item.Name, SourceType: item.SourceType, Source: item.Source})
		case !item.IsValidSkill:
			inv.invalid = append(inv.invalid, InvalidSkill{Name: item.Name, SourceType: item.SourceType, Source: item.Source})
			inv.present = append(inv.present, presentSkill{Name: item.Name, SourceType: item.SourceType, Source: item.Source})
		default:
			inv.present = append(inv.present, presentSkill{Name: item.Name, SourceType: item.SourceType, Source: item.Source})
		}
	}
	return inv, nil
}
