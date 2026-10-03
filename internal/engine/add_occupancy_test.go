package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

func TestInspectAddSlot(t *testing.T) {
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
		slot, err := InspectAddSlot(cfg, skillsDir, NewCommandAddSource("true", "", ""), name, ".")
		if err != nil {
			t.Fatal(err)
		}
		if got := slot.Occupancy; got != want {
			t.Errorf("InspectAddSlot(%q) = %d; want %d", name, got, want)
		}
	}
}

func TestAddOccupancyDifferentSourceConflictsWithoutDisk(t *testing.T) {
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "original/repo", "sample", "sample", "github", "main")
	slot, err := InspectAddSlot(cfg, t.TempDir(), AddSource{Kind: AddSourceRemote, Key: "new/repo"}, "sample", "sample")
	if err != nil {
		t.Fatal(err)
	}
	if got := slot.Occupancy; got != AddSlotConflict {
		t.Fatalf("occupancy = %v; want Conflict for the existing declaration", got)
	}
}

func TestAddSlotAndPlanShareSourceIdentityRules(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, "source")
	command := NewCommandAddSource("install sample", "", "")
	remote := AddSource{Kind: AddSourceRemote, Key: "original/repo"}
	symlink := NewSymlinkAddSource(local, "")
	tests := []struct {
		name     string
		kind     string
		existing string
		source   AddSource
		subpath  string
		want     AddOccupancy
	}{
		{"same remote", "remote", "original/repo", remote, "changed/path", AddSlotDeclared},
		{"different remote", "remote", "other/repo", remote, "sample", AddSlotConflict},
		{"same command", "command", "install sample", command, ".", AddSlotDeclared},
		{"different command", "command", "install other", command, ".", AddSlotConflict},
		{"same local root", "symlink", local, symlink, ".", AddSlotDeclared},
		{"same local subpath", "symlink", filepath.Join(local, "nested", "sample"), symlink, "nested/sample", AddSlotDeclared},
		{"different local subpath", "symlink", filepath.Join(local, "sample"), symlink, "nested/sample", AddSlotConflict},
		{"remote to command", "remote", "original/repo", command, ".", AddSlotConflict},
		{"command to local", "command", "install sample", symlink, ".", AddSlotConflict},
		{"local to remote", "symlink", local, remote, "sample", AddSlotConflict},
	}
	for _, tt := range tests {
		for _, present := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/present=%t", tt.name, present), func(t *testing.T) {
				skillsDir := filepath.Join(t.TempDir(), "skills")
				cfg := config.DefaultConfig()
				if tt.kind == "remote" {
					config.AddRemoteSkillEntry(cfg, tt.existing, "sample", "sample", "github", "main")
				} else {
					cfg.Local["sample"] = config.LocalEntry{Type: tt.kind, Source: tt.existing}
					if tt.kind == "command" {
						cfg.Local["sample"] = config.LocalEntry{Type: "command", Command: tt.existing}
					}
				}
				if present {
					if err := os.MkdirAll(filepath.Join(skillsDir, "sample"), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				slot, err := InspectAddSlot(cfg, skillsDir, tt.source, "sample", tt.subpath)
				if err != nil {
					t.Fatal(err)
				}
				if slot.Occupancy != tt.want {
					t.Fatalf("occupancy = %v; want %v", slot.Occupancy, tt.want)
				}
				plan, err := BuildAddPlan(cfg, filepath.Join(t.TempDir(), "skills.json"), skillsDir, tt.source, map[string]string{"sample": tt.subpath}, AddAvailabilityIntent{})
				if err != nil {
					t.Fatal(err)
				}
				if tt.want == AddSlotDeclared {
					if len(plan.Conflicts) != 0 {
						t.Fatalf("same Source has conflicts: %v", plan.Conflicts)
					}
				} else {
					expected := fmt.Sprintf("[%s] %s", tt.kind, models.ToTildePath(tt.existing))
					if len(plan.Conflicts) != 1 || plan.Conflicts[0].CurrentSrc != expected || slot.CurrentSrc != expected {
						t.Fatalf("slot=%v conflicts=%v; want Source %q", slot, plan.Conflicts, expected)
					}
				}
			})
		}
	}
}

func TestAddSlotMatchesPortableLocalSource(t *testing.T) {
	root := t.TempDir()
	skillsDir := filepath.Join(root, ".agents", "skills")
	local := filepath.Join(root, "source", "sample")
	cfg := config.DefaultConfig()
	cfg.Local["sample"] = config.LocalEntry{Type: "symlink", Source: models.StoreLocalSourcePath(local, skillsDir)}
	slot, err := InspectAddSlot(cfg, skillsDir, NewSymlinkAddSource(filepath.Dir(local), ""), "sample", "sample")
	if err != nil || slot.Occupancy != AddSlotDeclared {
		t.Fatalf("slot=%v err=%v; want same Source", slot, err)
	}
}

func TestAddPlanRejectsUninspectableOccupancy(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprintf("nested=%t", nested), func(t *testing.T) {
			blocker := filepath.Join(t.TempDir(), "not-a-directory")
			if err := os.WriteFile(blocker, []byte("keep"), 0o644); err != nil {
				t.Fatal(err)
			}
			skillsDir := blocker
			if nested {
				skillsDir = filepath.Join(blocker, "missing", "skills")
			}
			source := NewCommandAddSource("true", "", "")
			_, err := InspectAddSlot(nil, skillsDir, source, "sample", ".")
			if err == nil || !strings.Contains(err.Error(), `Skill "sample"`) {
				t.Fatalf("inspection error = %v", err)
			}
			plan, err := BuildAddPlan(nil, "unused.json", skillsDir, source, map[string]string{"sample": "."}, AddAvailabilityIntent{})
			if err == nil || plan.Skills != nil {
				t.Fatalf("plan=%v err=%v; want no applicable plan", plan, err)
			}
			if got, err := os.ReadFile(blocker); err != nil || string(got) != "keep" {
				t.Fatalf("occupancy was changed: %q, %v", got, err)
			}
		})
	}
}

func TestAddSlotIsFreeWhenScopeDirectoriesAreMissing(t *testing.T) {
	skillsDir := filepath.Join(t.TempDir(), "missing", ".agents", "skills")
	slot, err := InspectAddSlot(nil, skillsDir, NewCommandAddSource("true", "", ""), "sample", ".")
	if err != nil || slot.Occupancy != AddSlotFree {
		t.Fatalf("slot=%v err=%v; want Free", slot, err)
	}
	if _, err := os.Stat(skillsDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspection created directories: %v", err)
	}
}

func TestAddSlotChecksSymlinkedScopeDirectories(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(fmt.Sprintf("target-exists=%t", exists), func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "target")
			if exists {
				if err := os.Mkdir(target, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			link := filepath.Join(root, "skills")
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			slot, err := InspectAddSlot(nil, link, NewCommandAddSource("true", "", ""), "sample", ".")
			if exists {
				if err != nil || slot.Occupancy != AddSlotFree {
					t.Fatalf("slot=%v err=%v; want Free", slot, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), `Skill "sample"`) {
				t.Fatalf("inspection error = %v; a dangling parent is not a free slot", err)
			}
		})
	}
}
