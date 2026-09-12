package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestLoadInventoryClassifiesOccupancy(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "present", "present", "github", "")
	config.AddRemoteSkillEntry(cfg, "owner/repo", "missing", "missing", "github", "")
	config.AddLocalSymlinkEntry(cfg, "broken", filepath.Join(project, "src", "broken"), "")
	config.AddLocalSymlinkEntry(cfg, "stub", filepath.Join(project, "src", "stub"), "")
	config.AddLocalSymlinkEntry(cfg, "nested", filepath.Join(skillsDir, "nested"), "")

	writeSkill := func(name string) {
		t.Helper()
		dir := filepath.Join(skillsDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSkill("present")
	writeSkill("orphan")
	if err := os.MkdirAll(filepath.Join(skillsDir, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "stub"), []byte("../src/stub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(skillsDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "nested", "SKILL.md"), []byte("# Nested\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "gone"), filepath.Join(skillsDir, "leftover")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	inv, err := LoadInventory(cfg, skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inv.Missing(), []string{"missing"}) {
		t.Fatalf("Missing = %#v", inv.Missing())
	}
	if !reflect.DeepEqual(inv.Untracked(), []string{"orphan"}) {
		t.Fatalf("Untracked = %#v", inv.Untracked())
	}
	if !reflect.DeepEqual(inv.UntrackedLinks(), []string{"leftover"}) {
		t.Fatalf("UntrackedLinks = %#v", inv.UntrackedLinks())
	}
	if got := inv.Invalid(); len(got) != 1 || got[0].Name != "broken" {
		t.Fatalf("Invalid = %#v", inv.Invalid())
	}
	if !reflect.DeepEqual(inv.Stubs(), []string{"stub"}) {
		t.Fatalf("Stubs = %#v", inv.Stubs())
	}
	if got := inv.IllegalLocal(); len(got) != 1 || got[0].Name != "nested" {
		t.Fatalf("IllegalLocal = %#v", inv.IllegalLocal())
	}

	var present []string
	for _, skill := range inv.declaredPresent() {
		present = append(present, skill.Name)
	}
	for _, name := range []string{"present", "broken", "stub"} {
		if !slices.Contains(present, name) {
			t.Fatalf("declaredPresent = %#v; want %s", present, name)
		}
	}
	for _, name := range []string{"nested", "orphan", "leftover", "missing"} {
		if slices.Contains(present, name) {
			t.Fatalf("declaredPresent = %#v; %s must not appear", present, name)
		}
	}
	for _, item := range inv.SkillItems() {
		if item.Name == "orphan" && item.SourceType != "untracked" {
			t.Fatalf("projection SourceType for orphan = %q", item.SourceType)
		}
		if item.Name == "leftover" && item.SourceType != "symlink" {
			t.Fatalf("projection SourceType for leftover = %q", item.SourceType)
		}
	}
}
