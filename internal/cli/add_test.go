package cli

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/tui"
)

func TestMarkInstalledSkillsAcrossUndecidedScopes(t *testing.T) {
	globalDir := filepath.Join(t.TempDir(), "global")
	projectDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(projectDir, "installed"), 0o755); err != nil {
		t.Fatal(err)
	}
	options := []tui.SelectOption{
		{Key: "installed", Title: "installed"},
		{Key: "new", Title: "new"},
	}

	markInstalledSkills(options, []string{globalDir, projectDir})

	if !options[0].Installed || options[0].Selected {
		t.Fatalf("installed option = %#v; want installed but unselected", options[0])
	}
	if options[1].Installed || options[1].Selected {
		t.Fatalf("new option = %#v; want unselected", options[1])
	}
}

func TestGroupDiscoveredSkillsUsesSkillParentDirectories(t *testing.T) {
	discovered := map[string]string{
		"api":  "skills/engineering/api",
		"lint": "skills/engineering/lint",
		"ship": "skills/release/ship",
	}

	groups, ok := groupDiscoveredSkills(discovered)
	if !ok {
		t.Fatal("groupDiscoveredSkills did not group skills from different parent directories")
	}
	if len(groups) != 2 {
		t.Fatalf("group count = %d; want 2", len(groups))
	}
	if got := groups["engineering"]; len(got) != 2 || got[0].Key != "api" || got[1].Key != "lint" {
		t.Fatalf("engineering group = %#v; want api and lint", got)
	}
	if got := groups["release"]; len(got) != 1 || got[0].Key != "ship" {
		t.Fatalf("release group = %#v; want ship", got)
	}
}

func TestGroupDiscoveredSkillsSkipsSingleDirectoryAndDisambiguatesNames(t *testing.T) {
	t.Run("single directory", func(t *testing.T) {
		groups, ok := groupDiscoveredSkills(map[string]string{
			"api":  "skills/engineering/api",
			"lint": "skills/engineering/lint",
		})
		if ok || groups != nil {
			t.Fatalf("single-directory skills grouped as %#v", groups)
		}
	})

	t.Run("same final directory name", func(t *testing.T) {
		groups, ok := groupDiscoveredSkills(map[string]string{
			"first":  "skills/first/shared/first",
			"second": "skills/second/shared/second",
			"third":  "skills/other/third",
		})
		if !ok {
			t.Fatal("skills from different parent directories were not grouped")
		}
		if _, exists := groups["first/shared"]; !exists {
			t.Fatalf("groups = %#v; want first/shared", groups)
		}
		if _, exists := groups["second/shared"]; !exists {
			t.Fatalf("groups = %#v; want second/shared", groups)
		}
		if _, exists := groups["other"]; !exists {
			t.Fatalf("groups = %#v; want unambiguous final directory other", groups)
		}
	})

	t.Run("repository root", func(t *testing.T) {
		groups, ok := groupDiscoveredSkills(map[string]string{
			"root": "root-skill",
			"tool": "skills/tools/tool",
		})
		if !ok {
			t.Fatal("root and nested skills were not grouped")
		}
		if got := groups[repositoryRootGroup]; len(got) != 1 || got[0].Key != "root" {
			t.Fatalf("repository root group = %#v; want root", got)
		}
	})

	t.Run("reserved root label", func(t *testing.T) {
		groups, ok := groupDiscoveredSkills(map[string]string{
			"root":     "root-skill",
			"reserved": "Repository root/reserved",
		})
		if !ok {
			t.Fatal("root and reserved directory were not grouped")
		}
		if _, exists := groups[repositoryRootGroup]; !exists {
			t.Fatalf("groups = %#v; want repository root group", groups)
		}
		if _, exists := groups["./Repository root"]; !exists {
			t.Fatalf("groups = %#v; want disambiguated reserved directory", groups)
		}
	})
}

func TestDiscoveryResultPreservesDiscoveryError(t *testing.T) {
	discoveryErr := errors.New("discovery scope escapes repository")

	_, err := discoveryResult(nil, discoveryErr, "owner/repo")
	if err == nil || !strings.Contains(err.Error(), "owner/repo") || !strings.Contains(err.Error(), discoveryErr.Error()) {
		t.Fatalf("discovery result error = %v; want Source and cause", err)
	}
}

func TestAddRejectsAllWithNamedSkillsBeforeDiscovery(t *testing.T) {
	for _, args := range [][]string{
		{"--symlink", filepath.Join(t.TempDir(), "missing"), "--all", "--skill", "one"},
		{"command-skill", "--command", "echo ok", "--all"},
	} {
		cmd := newAddCmd()
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "--all cannot be combined") {
			t.Fatalf("args %v: error = %v; want conflicting selection request", args, err)
		}
	}
}

func TestSameAgentSelection(t *testing.T) {
	want := map[string]struct{}{"claude-code": {}, "continue": {}}
	if !sameAgentSelection(want, []string{"claude-code", "continue"}) {
		t.Fatal("identical sets must match regardless of input order")
	}
	if sameAgentSelection(want, []string{"claude-code"}) {
		t.Fatal("a shorter got must not match")
	}
	if sameAgentSelection(want, []string{"claude-code", "roo"}) {
		t.Fatal("a differing member must not match despite equal length")
	}
	if !sameAgentSelection(map[string]struct{}{}, nil) {
		t.Fatal("two empty selections must match")
	}
}

func TestAgentSelectionBaseline(t *testing.T) {
	skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

	t.Run("single skill reports its own Availability", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Settings.DefaultAgents = []string{"claude"}
		availability := engine.NewAvailability(cfg, skillsDir)

		baseline, ok := agentSelectionBaseline(availability, []string{"only"})
		if !ok {
			t.Fatal("a single skill can never conflict with itself")
		}
		if !reflect.DeepEqual(baseline, map[string]struct{}{"claude-code": {}}) {
			t.Fatalf("baseline = %#v", baseline)
		}
	})

	t.Run("agreeing skills report the shared Availability", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Settings.DefaultAgents = []string{"claude", "continue"}
		availability := engine.NewAvailability(cfg, skillsDir)

		baseline, ok := agentSelectionBaseline(availability, []string{"a", "b"})
		if !ok {
			t.Fatal("skills sharing the default policy must not conflict")
		}
		want := map[string]struct{}{"claude-code": {}, "continue": {}}
		if !reflect.DeepEqual(baseline, want) {
			t.Fatalf("baseline = %#v, want %#v", baseline, want)
		}
	})

	t.Run("disagreeing skills report a conflict", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Settings.DefaultAgents = []string{"claude"}
		cfg.Settings.Availability["b"] = config.AvailabilityOverride{Include: []string{"continue"}}
		availability := engine.NewAvailability(cfg, skillsDir)

		baseline, ok := agentSelectionBaseline(availability, []string{"a", "b"})
		if ok {
			t.Fatalf("expected a conflict, got baseline = %#v", baseline)
		}
		if baseline != nil {
			t.Fatalf("a conflict must not leak a partial baseline, got %#v", baseline)
		}
	})
}
