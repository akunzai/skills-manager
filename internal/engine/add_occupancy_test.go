package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestClassifyAddOccupancy(t *testing.T) {
	skillsDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Local["declared"] = config.LocalEntry{Type: "command", Command: "true"}
	for _, dir := range []string{"declared", "untracked-dir"} {
		if err := os.MkdirAll(filepath.Join(skillsDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(skillsDir, "gone"), filepath.Join(skillsDir, "dangling")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	for name, want := range map[string]AddOccupancy{
		"declared":      AddSlotDeclared,
		"untracked-dir": AddSlotConflict,
		"dangling":      AddSlotConflict,
		"new":           AddSlotFree,
	} {
		if got := ClassifyAddOccupancy(cfg, skillsDir, name); got != want {
			t.Errorf("ClassifyAddOccupancy(%q) = %d; want %d", name, got, want)
		}
	}
}
