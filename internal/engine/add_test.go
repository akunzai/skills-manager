package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestBuildAddPlanDetectsRemoteConflicts(t *testing.T) {
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "original/repo", "my-skill", "subpath", "github", "")

	source := NewRemoteAddSource("new/repo", "github", "", "/tmp/cache")
	plan := BuildAddPlan(cfg, "/tmp/skills.json", "/tmp/skills", source, map[string]string{"my-skill": "subpath"}, nil)

	if len(plan.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(plan.Conflicts))
	}
	conflict := plan.Conflicts[0]
	if conflict.Skill != "my-skill" {
		t.Errorf("expected skill my-skill, got %s", conflict.Skill)
	}
	if conflict.CurrentSrc != "[remote] original/repo" {
		t.Errorf("unexpected current source: %s", conflict.CurrentSrc)
	}
}

func TestBuildAddPlanDetectsUntrackedDiskConflicts(t *testing.T) {
	skillsDir := t.TempDir()
	untrackedDir := filepath.Join(skillsDir, "existing-skill")
	if err := os.MkdirAll(untrackedDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	source := NewSymlinkAddSource("/tmp/local-source", "local test", false)
	plan := BuildAddPlan(cfg, "/tmp/skills.json", skillsDir, source, map[string]string{"existing-skill": "."}, nil)

	if len(plan.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(plan.Conflicts))
	}
	conflict := plan.Conflicts[0]
	if conflict.Skill != "existing-skill" {
		t.Errorf("expected skill existing-skill, got %s", conflict.Skill)
	}
	if conflict.CurrentSrc != "[untracked directory]" {
		t.Errorf("expected [untracked directory], got %s", conflict.CurrentSrc)
	}
}

func TestResolveDiscoveredSkillsAllFlag(t *testing.T) {
	discovered := map[string]string{"a": "skills/a", "b": "skills/b"}
	got, unmatched, err := ResolveDiscoveredSkills(discovered, "/tmp", true, "", nil, false)
	if err != nil || len(unmatched) > 0 {
		t.Fatalf("unexpected error or unmatched: err=%v, unmatched=%v", err, unmatched)
	}
	if !reflect.DeepEqual(got, discovered) {
		t.Errorf("got %v, want %v", got, discovered)
	}
}

func TestResolveDiscoveredSkillsCaseInsensitiveMatching(t *testing.T) {
	discovered := map[string]string{"AwesomeSkill": "skills/awesome"}
	got, unmatched, err := ResolveDiscoveredSkills(discovered, "/tmp", false, "", []string{"awesomeskill"}, false)
	if err != nil || len(unmatched) > 0 {
		t.Fatalf("unexpected error or unmatched: err=%v, unmatched=%v", err, unmatched)
	}
	if got["AwesomeSkill"] != "skills/awesome" {
		t.Errorf("expected case-insensitive match for AwesomeSkill, got %v", got)
	}
}
