package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// AddOccupancy describes what an Add would replace.
type AddOccupancy int

const (
	// AddSlotFree has neither a declaration nor filesystem occupancy.
	AddSlotFree AddOccupancy = iota
	// AddSlotDeclared has the same declared kind and Source identity.
	AddSlotDeclared
	// AddSlotConflict replaces a different declaration or Untracked occupancy.
	AddSlotConflict
)

// AddSlot is the occupancy and, for a Conflict, the Source being replaced.
type AddSlot struct {
	Occupancy  AddOccupancy
	CurrentSrc string
}

// InspectAddSlot compares a proposed Skill with the Scope's declaration before
// inspecting Untracked occupancy. A missing copy does not erase a declaration.
// Errors inspecting occupancy are returned rather than treated as a free slot.
func InspectAddSlot(cfg *config.Config, skillsDir string, source AddSource, name, subpath string) (AddSlot, error) {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	if skillsDir == "" {
		skillsDir = models.DefaultSkillsDir()
	}
	kind, srcKey, found := config.FindSkillSource(cfg, name)
	if found {
		entry := cfg.Local[name]
		current := ""
		same := false
		switch kind {
		case config.SkillRemote:
			current = fmt.Sprintf("[remote] %s", srcKey)
			same = source.Kind == AddSourceRemote && srcKey == source.Key
		case config.SkillCommand:
			current = fmt.Sprintf("[command] %s", entry.Command)
			same = source.Kind == AddSourceCommand && entry.Command == source.Command
		case config.SkillSymlink:
			localSkillSource := source.LocalPath
			if subpath != "" && subpath != "." {
				localSkillSource = filepath.Join(source.LocalPath, filepath.FromSlash(subpath))
			}
			stored := models.StoreLocalSourcePath(localSkillSource, skillsDir)
			current = fmt.Sprintf("[symlink] %s", models.ToTildePath(entry.Source))
			same = source.Kind == AddSourceSymlink && (entry.Source == stored || models.ToTildePath(entry.Source) == models.ToTildePath(localSkillSource))
		}
		if same {
			return AddSlot{Occupancy: AddSlotDeclared}, nil
		}
		return AddSlot{Occupancy: AddSlotConflict, CurrentSrc: current}, nil
	}
	targetPath := filepath.Join(skillsDir, name)
	fi, err := os.Lstat(targetPath)
	if errors.Is(err, os.ErrNotExist) {
		return AddSlot{Occupancy: AddSlotFree}, nil
	}
	if err != nil {
		return AddSlot{}, fmt.Errorf("inspect occupancy for Skill %q: %w", name, err)
	}
	current := "[untracked directory]"
	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(targetPath)
		if err != nil {
			return AddSlot{}, fmt.Errorf("read occupied symlink for Skill %q: %w", name, err)
		}
		current = fmt.Sprintf("[symlink] %s", models.ToTildePath(target))
	}
	return AddSlot{Occupancy: AddSlotConflict, CurrentSrc: current}, nil
}
