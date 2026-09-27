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
	untrackedDirs  []string
	untrackedLinks []string
	invalid        []InvalidSkill
	stubs          []string
	illegalLocal   []IllegalLocalSource
	present        []presentSkill
	items          []models.SkillItem
	skillsDir      string
}

type presentSkill struct {
	Name       string
	SourceType string
	Source     string
}

func (inv Inventory) Missing() []string { return slices.Clone(inv.missing) }

func (inv Inventory) Untracked() []string { return slices.Clone(inv.untracked) }

// UntrackedDirectories is the Untracked occupancy that is a real directory:
// the user's own content, which only an explicit choice removes.
func (inv Inventory) UntrackedDirectories() []string { return slices.Clone(inv.untrackedDirs) }

func (inv Inventory) UntrackedLinks() []string { return slices.Clone(inv.untrackedLinks) }

func (inv Inventory) Invalid() []InvalidSkill { return slices.Clone(inv.invalid) }

func (inv Inventory) Stubs() []string { return slices.Clone(inv.stubs) }

func (inv Inventory) IllegalLocal() []IllegalLocalSource { return slices.Clone(inv.illegalLocal) }

// SkillItems projects classified occupancy into the engine row a frontend
// builds its own presentation from. Callers that decide from occupancy use
// the typed queries instead of SourceType. baselines fills Signed and
// Unverified, the same rule ls has always used: both are true only for an
// installed Skill, since only Sync's Apply records a Baseline. A nil
// baselines leaves them false, as when the caller has no Scope state to
// compare against.
func (inv Inventory) SkillItems(baselines *Baselines) []models.SkillItem {
	items := slices.Clone(inv.items)
	if baselines == nil {
		return items
	}
	for i := range items {
		applied, _ := baselines.Applied(items[i].Name)
		items[i].Signed = items[i].IsInstalled && applied.Signed
		items[i].Unverified = items[i].IsInstalled && applied.Unverified != ""
	}
	return items
}

// AvailableTo is the Skills one Agent sees: every installed Skill for an
// Automatically available Agent, since it reads the skills directory
// directly and needs no per-Skill Availability, and otherwise the Skills
// whose Availability names it.
func (inv Inventory) AvailableTo(agent string) []models.SkillItem {
	filterAgent := models.NormalizeAgentName(agent)
	agents := models.ForSkillsDir(inv.skillsDir)
	seesEveryInstalled := agents.IsAutomatic(filterAgent)
	var out []models.SkillItem
	for _, item := range inv.items {
		switch {
		case seesEveryInstalled:
			if item.IsInstalled {
				out = append(out, item)
			}
		case slices.ContainsFunc(item.Agents, func(a string) bool { return models.NormalizeAgentName(a) == filterAgent }):
			out = append(out, item)
		}
	}
	return out
}

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
		// A Skill not on disk still gets the path it would take there, so a
		// frontend never has to fall back to it itself.
		if item.InstalledPath == "" {
			item.InstalledPath = filepath.Join(baseSkills, item.Name)
		}
		result = append(result, *item)
	}

	slices.SortFunc(result, func(a, b models.SkillItem) int {
		return cmp.Or(
			cmp.Compare(strings.ToLower(a.Source), strings.ToLower(b.Source)),
			cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
		)
	})

	inv := Inventory{items: result, skillsDir: baseSkills}
	for i := range result {
		item := &result[i]
		mode := kind[item.Name]
		switch {
		case !item.IsInstalled:
			item.Status = models.SkillStatusMissing
			inv.missing = append(inv.missing, item.Name)
		case item.SourceType == "symlink":
			item.Status = models.SkillStatusUntrackedLink
			inv.untrackedLinks = append(inv.untrackedLinks, item.Name)
		case item.SourceType == "untracked":
			item.Status = models.SkillStatusUntracked
			inv.untracked = append(inv.untracked, item.Name)
			if mode.IsDir() {
				inv.untrackedDirs = append(inv.untrackedDirs, item.Name)
			}
		case item.SourceType == "local_symlink" && models.LocalSourceInsideSkillsDir(models.ResolveLocalSourcePath(item.Source, baseSkills), baseSkills) && mode&os.ModeSymlink == 0 && mode != 0:
			item.Status = models.SkillStatusIllegalLocal
			inv.illegalLocal = append(inv.illegalLocal, IllegalLocalSource{Name: item.Name, Source: item.Source})
		case item.SourceType == "local_symlink" && mode.IsRegular():
			item.Status = models.SkillStatusStub
			inv.stubs = append(inv.stubs, item.Name)
			inv.present = append(inv.present, presentSkill{Name: item.Name, SourceType: item.SourceType, Source: item.Source})
		case !item.IsValidSkill:
			item.Status = models.SkillStatusInvalid
			inv.invalid = append(inv.invalid, InvalidSkill{Name: item.Name, SourceType: item.SourceType, Source: item.Source})
			inv.present = append(inv.present, presentSkill{Name: item.Name, SourceType: item.SourceType, Source: item.Source})
		default:
			item.Status = models.SkillStatusPresent
			inv.present = append(inv.present, presentSkill{Name: item.Name, SourceType: item.SourceType, Source: item.Source})
		}
	}
	return inv, nil
}
