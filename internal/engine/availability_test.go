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
	config.AddLocalSymlinkEntry(cfg, skill, filepath.Join(skillsDir, skill), "")
	return NewAvailability(cfg, skillsDir), filepath.Join(project, ".claude", "skills", skill), skillsDir
}

// The Drift branches doctor and sync act on differently: a missing link is
// repaired silently, a foreign path needs the user's consent before it is
// replaced, and an unobservable one cannot be repaired at all. Each was
// reachable only through the CLI before this.
func TestOccupancyDriftDistinguishesDriftBranches(t *testing.T) {
	t.Run("missing link", func(t *testing.T) {
		availability, _, _ := projectAvailability(t, "sample")

		drift := availability.ObserveOccupancy().Drift("sample")

		if !reflect.DeepEqual(drift.Missing, []string{"claude-code"}) {
			t.Fatalf("Missing = %#v; want claude-code", drift.Missing)
		}
		if len(drift.Foreign) != 0 || len(drift.Unobservable) != 0 {
			t.Fatalf("a link that is merely absent must not read as foreign or unobservable: %#v", drift)
		}
	})

	t.Run("declared link is present and managed", func(t *testing.T) {
		availability, _, _ := projectAvailability(t, "sample")
		if _, err := availability.Apply("sample"); err != nil {
			t.Fatal(err)
		}

		if drift := availability.ObserveOccupancy().Drift("sample"); !drift.Empty() {
			t.Fatalf("Drift after Apply = %#v; want none", drift)
		}
	})

	t.Run("foreign directory", func(t *testing.T) {
		availability, linkPath, _ := projectAvailability(t, "sample")
		if err := os.MkdirAll(linkPath, 0o755); err != nil {
			t.Fatal(err)
		}

		drift := availability.ObserveOccupancy().Drift("sample")

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

		drift := availability.ObserveOccupancy().Drift("sample")

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

		drift := availability.ObserveOccupancy().Drift("sample")

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
		if _, err := availability.Apply("sample"); err != nil {
			t.Fatal(err)
		}
		if err := availability.Exclude("sample", "claude"); err != nil {
			t.Fatal(err)
		}

		drift := availability.ObserveOccupancy().Drift("sample")

		if !reflect.DeepEqual(drift.Unexpected, []string{"claude-code"}) {
			t.Fatalf("Unexpected = %#v; want the link left behind by the excluded Agent", drift.Unexpected)
		}
	})
}

// Sync plans from the Drift verdict and then applies, so the verdict must
// say what Apply does: reconcile a link, refuse a path, or leave a copy be.
func TestDriftVerdictMatchesApply(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		setup                 func(t *testing.T, availability *Availability, linkPath string)
		reconcilable, refused bool
	}{
		{"missing link", func(*testing.T, *Availability, string) {}, true, false},
		{"unexpected link", func(t *testing.T, availability *Availability, _ string) {
			if _, err := availability.Apply("sample"); err != nil {
				t.Fatal(err)
			}
			if err := availability.Exclude("sample", "claude"); err != nil {
				t.Fatal(err)
			}
		}, true, false},
		{"foreign directory", func(t *testing.T, _ *Availability, linkPath string) {
			if err := os.MkdirAll(linkPath, 0o755); err != nil {
				t.Fatal(err)
			}
		}, false, true},
		{"unobservable agent directory", func(t *testing.T, _ *Availability, linkPath string) {
			agentDir := filepath.Dir(linkPath)
			if err := os.MkdirAll(filepath.Dir(agentDir), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(agentDir, []byte("not a directory\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, false, true},
		{"copy", func(t *testing.T, availability *Availability, _ string) {
			denyLinkCreation(t, ErrLinkPrivilegeNotHeld)
			if _, err := availability.Apply("sample"); err != nil {
				t.Fatal(err)
			}
		}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			availability, linkPath, _ := projectAvailability(t, "sample")
			tc.setup(t, availability, linkPath)

			drift := availability.ObserveOccupancy().Drift("sample")
			if drift.Reconcilable() != tc.reconcilable || drift.Refused() != tc.refused {
				t.Fatalf("Drift %#v: Reconcilable = %v, Refused = %v; want %v, %v", drift, drift.Reconcilable(), drift.Refused(), tc.reconcilable, tc.refused)
			}
			_, err := availability.Apply("sample")
			if refused := err != nil; refused != tc.refused {
				t.Fatalf("Apply error = %v; want refused = %v", err, tc.refused)
			}
			if !tc.refused {
				if after := availability.ObserveOccupancy().Drift("sample"); !after.Empty() {
					t.Fatalf("Drift after Apply = %#v; want none", after)
				}
			}
		})
	}
}

// ReplaceForeign is the one mutation that removes a path the user did not
// declare, so it refuses to act on a diagnosis the filesystem has moved past.
func TestReplaceForeignRefusesAStaleDiagnosis(t *testing.T) {
	availability, linkPath, _ := projectAvailability(t, "sample")
	if err := os.MkdirAll(linkPath, 0o755); err != nil {
		t.Fatal(err)
	}
	diagnosed := availability.ObserveOccupancy().Drift("sample").Foreign

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

func plantManagedLink(t *testing.T, skillsDir, agentDir, skill string) string {
	t.Helper()
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	master := filepath.Join(skillsDir, skill)
	rel, err := filepath.Rel(agentDir, master)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(agentDir, skill)
	if err := os.Symlink(rel, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return link
}

func TestOccupancyDriftReportsBrokenDesiredLink(t *testing.T) {
	availability, linkPath, skillsDir := projectAvailability(t, "sample")
	plantManagedLink(t, skillsDir, filepath.Dir(linkPath), "sample")
	if err := os.RemoveAll(filepath.Join(skillsDir, "sample")); err != nil {
		t.Fatal(err)
	}

	drift := availability.ObserveOccupancy().Drift("sample")

	if !reflect.DeepEqual(drift.Broken, []string{"claude-code"}) {
		t.Fatalf("Broken = %#v; want claude-code", drift.Broken)
	}
	if drift.Empty() {
		t.Fatal("Broken is Drift; Empty must be false")
	}
	if len(drift.Missing) != 0 {
		t.Fatalf("a dangling managed path is Broken, not Missing: %#v", drift.Missing)
	}
}

func TestApplyRepairsBrokenDesiredLink(t *testing.T) {
	availability, linkPath, skillsDir := projectAvailability(t, "sample")
	agentDir := filepath.Dir(linkPath)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// filepath.Join would Clean this; the extra segment must stay so Stat
	// fails while Abs(Clean(target)) still names the master Skill.
	rel, err := filepath.Rel(agentDir, filepath.Join(skillsDir, "sample"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rel+string(os.PathSeparator)+"missing"+string(os.PathSeparator)+"..", linkPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// Win32 GetFullPathName removes ".." without requiring the skipped
	// segment to exist (https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file),
	// so this target is a live link there and cannot represent Broken.
	if _, err := os.Stat(linkPath); err == nil {
		t.Skip("this platform canonicalizes extra .. segments, so the planted link is live")
	}
	if drift := availability.ObserveOccupancy().Drift("sample"); !reflect.DeepEqual(drift.Broken, []string{"claude-code"}) {
		t.Fatalf("Broken before Apply = %#v", drift)
	}

	if _, err := availability.Apply("sample"); err != nil {
		t.Fatal(err)
	}
	if drift := availability.ObserveOccupancy().Drift("sample"); !drift.Empty() {
		t.Fatalf("after Apply, drift = %#v; want none", drift)
	}
	if _, err := os.Stat(linkPath); err != nil {
		t.Fatalf("repaired Availability must resolve: %v", err)
	}
}

func TestOccupancyDriftCopiesDoNotFillEmpty(t *testing.T) {
	availability, linkPath, skillsDir := projectAvailability(t, "sample")
	denyLinkCreation(t, ErrLinkPrivilegeNotHeld)
	if err := os.WriteFile(filepath.Join(skillsDir, "sample", "SKILL.md"), []byte("# Sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := availability.Apply("sample"); err != nil {
		t.Fatal(err)
	}
	drift := availability.ObserveOccupancy().Drift("sample")
	if !reflect.DeepEqual(drift.Copies, []string{"claude-code"}) {
		t.Fatalf("Copies = %#v; want claude-code", drift.Copies)
	}
	if !drift.Empty() {
		t.Fatalf("Copies are working Availability, not Drift: %#v", drift)
	}
	if _, err := os.Stat(linkPath); err != nil {
		t.Fatalf("copy path missing: %v", err)
	}
}

// reservedNameScope is a Project Scope declaring a Skill named "synced" for
// Claude Code and Continue, while Claude Code keeps its own claude.ai skills
// in Synced/ — the name it reserves in any capitalization. It returns the
// Scope's Availability, its skills directory, and the file inside Claude
// Code's directory that nothing may touch.
func reservedNameScope(t *testing.T) (*Availability, *config.Config, string, string) {
	t.Helper()
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	mustWriteScopeStateTestFile(t, filepath.Join(skillsDir, "synced", "SKILL.md"), []byte("# Synced\n"))
	claudeFile := filepath.Join(project, ".claude", "skills", "Synced", "account", "SKILL.md")
	mustWriteScopeStateTestFile(t, claudeFile, []byte("# claude.ai skill\n"))
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude", "continue"}
	config.AddRemoteSkillEntry(cfg, "owner/repo", "synced", "synced", "github", "main")
	return NewAvailability(cfg, skillsDir), cfg, skillsDir, claudeFile
}

func assertClaudeReservedDirIntact(t *testing.T, claudeFile string) {
	t.Helper()
	got, err := os.ReadFile(claudeFile)
	if err != nil || string(got) != "# claude.ai skill\n" {
		t.Fatalf("Claude Code's reserved directory was touched: %q, %v", got, err)
	}
	entries, err := os.ReadDir(filepath.Dir(filepath.Dir(filepath.Dir(claudeFile))))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "Synced" {
			t.Fatalf("Claude Code's skills directory gained %q beside its reserved entry", entry.Name())
		}
	}
}

// A Skill whose name an Agent reserves is never Availability for that Agent:
// there is nothing to observe on that path, so it is neither Missing nor
// Foreign nor anything else.
func TestOccupancyDriftIgnoresAgentReservedName(t *testing.T) {
	availability, _, _, _ := reservedNameScope(t)

	drift := availability.ObserveOccupancy().Drift("synced")

	want := AvailabilityDrift{Skill: "synced", Missing: []string{"continue"}}
	if !reflect.DeepEqual(drift, want) {
		t.Fatalf("drift = %#v; want %#v", drift, want)
	}
}

func TestApplyLeavesAgentReservedNameAlone(t *testing.T) {
	availability, _, skillsDir, claudeFile := reservedNameScope(t)
	project := filepath.Dir(filepath.Dir(skillsDir))

	if _, err := availability.Apply("synced"); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	assertClaudeReservedDirIntact(t, claudeFile)
	if !isManagedSkillPath(filepath.Join(project, ".continue", "skills", "synced"), "synced", skillsDir) {
		t.Fatal("Apply did not make synced available to continue")
	}
}
