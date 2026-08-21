package cli

import (
	"errors"
	"strings"
	"testing"
)

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

func TestDiscoveryResultPreservesDuplicateNameError(t *testing.T) {
	duplicate := errors.New("duplicate skill name \"duplicate\" found in skills/first, skills/second")

	_, err := discoveryResult(nil, duplicate, "owner/repo")
	if err == nil || !strings.Contains(err.Error(), "skills/first") || !strings.Contains(err.Error(), "skills/second") {
		t.Fatalf("discovery result error = %v; want duplicate paths", err)
	}
}

func TestShouldPromptForDiscoveredSkills(t *testing.T) {
	if !shouldPromptForDiscoveredSkills(1, true, false) {
		t.Fatal("an interactive single-skill add must ask for confirmation")
	}
	if shouldPromptForDiscoveredSkills(1, false, false) {
		t.Fatal("a non-interactive add cannot prompt")
	}
	if shouldPromptForDiscoveredSkills(1, true, true) {
		t.Fatal("--yes must skip confirmation")
	}
}
