package engine

import (
	"os"
	"path/filepath"

	"github.com/akunzai/skills-manager/internal/config"
)

// AddOccupancy is what already holds the place an Add would Materialize a
// Skill into.
type AddOccupancy int

const (
	// AddSlotFree is a place nothing occupies.
	AddSlotFree AddOccupancy = iota
	// AddSlotDeclared is a place a Skill Config declares occupies.
	AddSlotDeclared
	// AddSlotConflict is Untracked occupancy: a real directory or a symlink,
	// dangling or not, that Config does not declare. BuildAddPlan reports it
	// as a Conflict.
	AddSlotConflict
)

// ClassifyAddOccupancy is what holds name's place on skillsDir, read with
// Lstat as BuildAddPlan reads it, so a person choosing Skills sees the same
// occupancy the plan will act on.
func ClassifyAddOccupancy(cfg *config.Config, skillsDir, name string) AddOccupancy {
	if _, err := os.Lstat(filepath.Join(skillsDir, name)); err != nil {
		return AddSlotFree
	}
	if _, _, declared := config.FindSkillSource(cfg, name); declared {
		return AddSlotDeclared
	}
	return AddSlotConflict
}
