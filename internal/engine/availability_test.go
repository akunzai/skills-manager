package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestAvailabilitySetManagedAgentsStoresMinimalOverride(t *testing.T) {
	cfg := config.DefaultConfig()
	availability := NewAvailability(cfg, filepath.Join(t.TempDir(), ".agents", "skills"))

	if err := availability.SetManagedAgents("sample", []string{"continue"}); err != nil {
		t.Fatal(err)
	}
	want := config.AvailabilityOverride{Include: []string{"continue"}, Exclude: []string{"claude-code"}}
	if got := cfg.Settings.Availability["sample"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("override = %#v, want %#v", got, want)
	}
	if got := availability.ManagedAgents("sample"); !reflect.DeepEqual(got, []string{"continue"}) {
		t.Fatalf("managed Agents = %#v", got)
	}
}

func TestAvailabilityMutationsRemainConflictFree(t *testing.T) {
	cfg := config.DefaultConfig()
	availability := NewAvailability(cfg, filepath.Join(t.TempDir(), ".agents", "skills"))
	if err := availability.Exclude("sample", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := availability.Include("sample", "claude", "continue"); err != nil {
		t.Fatal(err)
	}
	override := cfg.Settings.Availability["sample"]
	if !reflect.DeepEqual(override.Include, []string{"claude-code", "continue"}) || len(override.Exclude) != 0 {
		t.Fatalf("override = %#v", override)
	}
	if err := availability.Reset("sample", "claude"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Settings.Availability["sample"].Include, []string{"continue"}) {
		t.Fatalf("reset override = %#v", cfg.Settings.Availability["sample"])
	}
	availability.FollowDefaults("sample")
	if _, ok := cfg.Settings.Availability["sample"]; ok {
		t.Fatal("follow-defaults did not clear override")
	}
}

func TestAvailabilityUnknownAgentReferencesIncludesPolicyFields(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"codex", "mystery"}
	cfg.Settings.Availability["sample"] = config.AvailabilityOverride{Include: []string{"wat"}, Exclude: []string{"ghost"}}
	availability := NewAvailability(cfg, filepath.Join(t.TempDir(), ".agents", "skills"))

	want := []UnknownAgentReference{
		{Field: AgentRefDefaultAgents, Agent: "mystery"},
		{Skill: "sample", Field: AgentRefInclude, Agent: "wat"},
		{Skill: "sample", Field: AgentRefExclude, Agent: "ghost"},
	}
	if got := availability.UnknownAgentReferences(); !reflect.DeepEqual(got, want) {
		t.Fatalf("unknown references = %#v, want %#v", got, want)
	}
}

func TestAvailabilitySetManagedAgentsRejectsAutomaticallyAvailableAgent(t *testing.T) {
	cfg := config.DefaultConfig()
	availability := NewAvailability(cfg, filepath.Join(t.TempDir(), ".agents", "skills"))

	if err := availability.SetManagedAgents("sample", []string{"codex"}); err == nil {
		t.Fatal("Automatically available Agent must not become a managed selection")
	}
}

func TestAvailabilityMutationsRejectUnknownAgent(t *testing.T) {
	cfg := config.DefaultConfig()
	availability := NewAvailability(cfg, filepath.Join(t.TempDir(), ".agents", "skills"))

	if err := availability.Include("sample", "mystery"); err == nil {
		t.Fatal("expected unknown Agent mutation to fail")
	}
	if _, ok := cfg.Settings.Availability["sample"]; ok {
		t.Fatal("failed mutation changed Config")
	}
}

// projectAvailability declares one Skill for claude-code in a Project Scope and
// returns the Availability plus the agent link path Drift is measured against.
func projectAvailability(t *testing.T, skill string) (*Availability, string, string) {
	t.Helper()
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, skill), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	return NewAvailability(cfg, skillsDir), filepath.Join(project, ".claude", "skills", skill), skillsDir
}

// The Drift branches doctor and sync act on differently: a missing link is
// repaired silently, a foreign path needs the user's consent before it is
// replaced, and an unobservable one cannot be repaired at all. Each was
// reachable only through the CLI before this.
func TestObserveAvailabilityDistinguishesDriftBranches(t *testing.T) {
	t.Run("missing link", func(t *testing.T) {
		availability, _, _ := projectAvailability(t, "sample")

		drift := availability.ObserveAvailability("sample")

		if !reflect.DeepEqual(drift.Missing, []string{"claude-code"}) {
			t.Fatalf("Missing = %#v; want claude-code", drift.Missing)
		}
		if len(drift.Foreign) != 0 || len(drift.Unobservable) != 0 {
			t.Fatalf("a link that is merely absent must not read as foreign or unobservable: %#v", drift)
		}
	})

	t.Run("declared link is present and managed", func(t *testing.T) {
		availability, _, _ := projectAvailability(t, "sample")
		if err := availability.Apply("sample"); err != nil {
			t.Fatal(err)
		}

		if drift := availability.ObserveAvailability("sample"); !drift.Empty() {
			t.Fatalf("Drift after Apply = %#v; want none", drift)
		}
	})

	t.Run("foreign directory", func(t *testing.T) {
		availability, linkPath, _ := projectAvailability(t, "sample")
		if err := os.MkdirAll(linkPath, 0o755); err != nil {
			t.Fatal(err)
		}

		drift := availability.ObserveAvailability("sample")

		if len(drift.Foreign) != 1 {
			t.Fatalf("Foreign = %#v; want the unmanaged directory", drift.Foreign)
		}
		if got := drift.Foreign[0]; got.Kind != ForeignAvailabilityDirectory || got.Agent != "claude-code" {
			t.Fatalf("Foreign[0] = %#v; want a claude-code directory", got)
		}
		if len(drift.Missing) != 0 {
			t.Fatalf("an occupied path is not a missing one: %#v", drift.Missing)
		}
	})

	t.Run("foreign symlink keeps its target for the detail line", func(t *testing.T) {
		availability, linkPath, _ := projectAvailability(t, "sample")
		elsewhere := filepath.Join(t.TempDir(), "somewhere-else")
		if err := os.MkdirAll(elsewhere, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, linkPath); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}

		drift := availability.ObserveAvailability("sample")

		if len(drift.Foreign) != 1 {
			t.Fatalf("Foreign = %#v; want the unmanaged symlink", drift.Foreign)
		}
		if got := drift.Foreign[0]; got.Kind != ForeignAvailabilitySymlink || got.Target != elsewhere {
			t.Fatalf("Foreign[0] = %#v; want a symlink carrying its target", got)
		}
	})

	t.Run("unobservable agent directory", func(t *testing.T) {
		availability, linkPath, _ := projectAvailability(t, "sample")
		agentDir := filepath.Dir(linkPath)
		if err := os.MkdirAll(filepath.Dir(agentDir), 0o755); err != nil {
			t.Fatal(err)
		}
		// A regular file where the Agent directory belongs: Lstat below it
		// fails with ENOTDIR, which is neither absent nor readable.
		if err := os.WriteFile(agentDir, []byte("not a directory\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		drift := availability.ObserveAvailability("sample")

		if len(drift.Unobservable) != 1 {
			t.Fatalf("Unobservable = %#v; want the unreadable path", drift.Unobservable)
		}
		got := drift.Unobservable[0]
		if got.Agent != "claude-code" || got.Dir != agentDir || got.Err == "" {
			t.Fatalf("Unobservable[0] = %#v; want the agent, its directory and a reason", got)
		}
		if len(drift.Missing) != 0 || len(drift.Foreign) != 0 {
			t.Fatalf("an unreadable path must not be reported as missing or foreign: %#v", drift)
		}
	})

	t.Run("unexpected managed link after the agent is excluded", func(t *testing.T) {
		availability, _, _ := projectAvailability(t, "sample")
		if err := availability.Apply("sample"); err != nil {
			t.Fatal(err)
		}
		if err := availability.Exclude("sample", "claude"); err != nil {
			t.Fatal(err)
		}

		drift := availability.ObserveAvailability("sample")

		if !reflect.DeepEqual(drift.Unexpected, []string{"claude-code"}) {
			t.Fatalf("Unexpected = %#v; want the link left behind by the excluded Agent", drift.Unexpected)
		}
	})
}

// ReplaceForeign is the one mutation that removes a path the user did not
// declare, so it refuses to act on a diagnosis the filesystem has moved past.
func TestReplaceForeignRefusesAStaleDiagnosis(t *testing.T) {
	availability, linkPath, _ := projectAvailability(t, "sample")
	if err := os.MkdirAll(linkPath, 0o755); err != nil {
		t.Fatal(err)
	}
	diagnosed := availability.ObserveAvailability("sample").Foreign

	// The directory becomes a file between diagnosis and confirmation.
	if err := os.RemoveAll(linkPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(linkPath, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := availability.ReplaceForeign("sample", diagnosed); err == nil {
		t.Fatal("ReplaceForeign accepted a diagnosis the filesystem had moved past")
	}
	if _, err := os.Stat(linkPath); err != nil {
		t.Fatalf("the refused path must be left untouched: %v", err)
	}
}
