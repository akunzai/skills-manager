package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
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
	// Each item carries the class it was listed under, so ls words it the
	// way Doctor does.
	statuses := make(map[string]models.SkillStatus)
	for _, item := range inv.SkillItems(nil) {
		statuses[item.Name] = item.Status
	}
	wantStatuses := map[string]models.SkillStatus{
		"present": models.SkillStatusPresent, "missing": models.SkillStatusMissing,
		"broken": models.SkillStatusInvalid, "stub": models.SkillStatusStub,
		"nested": models.SkillStatusIllegalLocal, "orphan": models.SkillStatusUntracked,
		"leftover": models.SkillStatusUntrackedLink,
	}
	if !reflect.DeepEqual(statuses, wantStatuses) {
		t.Fatalf("statuses = %#v; want %#v", statuses, wantStatuses)
	}
	for _, item := range inv.SkillItems(nil) {
		if item.Name == "orphan" && item.SourceType != "untracked" {
			t.Fatalf("projection SourceType for orphan = %q", item.SourceType)
		}
		if item.Name == "leftover" && item.SourceType != "symlink" {
			t.Fatalf("projection SourceType for leftover = %q", item.SourceType)
		}
	}
}

// SkillItems fills Signed and Unverified from Baselines the same way ls has
// always read them: both true only for an installed Skill, since only
// Sync's Apply records one. A nil Baselines, as a caller with no Scope state
// to compare against passes, leaves them false. InstalledPath is always set,
// even for a Skill LoadInventory never found on disk.
func TestSkillItemsFillsSignedUnverifiedAndInstalledPathFromBaselines(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "signed", "signed", "github", "")
	config.AddRemoteSkillEntry(cfg, "owner/repo", "untrusted", "untrusted", "github", "")
	config.AddRemoteSkillEntry(cfg, "owner/repo", "missing", "missing", "github", "")

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
	writeSkill("signed")
	writeSkill("untrusted")

	baselines := OpenBaselines(skillsDir)
	signed := SkillFreshness{Name: "signed", Source: "owner/repo", ScopePath: filepath.Join(skillsDir, "signed")}
	if err := baselines.Record(signed, "cache", "abc123", true, ""); err != nil {
		t.Fatal(err)
	}
	untrusted := SkillFreshness{Name: "untrusted", Source: "owner/repo", ScopePath: filepath.Join(skillsDir, "untrusted")}
	if err := baselines.Record(untrusted, "cache", "def456", false, "SKILL.md changed after signing"); err != nil {
		t.Fatal(err)
	}

	inv, err := LoadInventory(cfg, skillsDir)
	if err != nil {
		t.Fatal(err)
	}

	byName := make(map[string]models.SkillItem)
	for _, item := range inv.SkillItems(OpenBaselines(skillsDir)) {
		byName[item.Name] = item
	}

	if got := byName["signed"]; !got.Signed || got.Unverified {
		t.Fatalf("signed = %+v; want Signed true, Unverified false", got)
	}
	if got := byName["untrusted"]; got.Signed || !got.Unverified {
		t.Fatalf("untrusted = %+v; want Signed false, Unverified true", got)
	}
	missing := byName["missing"]
	if missing.Signed || missing.Unverified {
		t.Fatalf("missing = %+v; an uninstalled Skill has no applied copy to be Signed or Unverified", missing)
	}
	if want := filepath.Join(skillsDir, "missing"); missing.InstalledPath != want {
		t.Fatalf("missing.InstalledPath = %q; want %q", missing.InstalledPath, want)
	}

	for _, item := range inv.SkillItems(nil) {
		if item.Signed || item.Unverified {
			t.Fatalf("SkillItems(nil) must not fill Signed or Unverified: %+v", item)
		}
	}
}

// AvailableTo answers which Skills one Agent sees: every installed Skill for
// an Automatically available Agent (gemini-cli reads the skills directory
// directly), and otherwise only the Skills whose declared Availability
// names it, installed or not.
func TestAvailableToIncludesAutomaticAgentsAndDeclaredAvailability(t *testing.T) {
	project := t.TempDir()
	skillsDir := filepath.Join(project, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Settings.DefaultAgents = []string{"claude"}
	config.AddRemoteSkillEntry(cfg, "owner/repo", "installed", "installed", "github", "")
	config.AddRemoteSkillEntry(cfg, "owner/repo", "not-installed", "not-installed", "github", "")

	dir := filepath.Join(skillsDir, "installed")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# Installed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	inv, err := LoadInventory(cfg, skillsDir)
	if err != nil {
		t.Fatal(err)
	}

	names := func(items []models.SkillItem) []string {
		out := make([]string, len(items))
		for i, item := range items {
			out[i] = item.Name
		}
		slices.Sort(out)
		return out
	}

	if got := names(inv.AvailableTo("gemini")); !slices.Equal(got, []string{"installed"}) {
		t.Fatalf("AvailableTo(gemini) = %#v; want [installed], an Automatic Agent sees disk state, not Availability", got)
	}
	if got := names(inv.AvailableTo("claude")); !slices.Equal(got, []string{"installed", "not-installed"}) {
		t.Fatalf("AvailableTo(claude) = %#v; want both, defaults declare both regardless of disk state", got)
	}
	if got := inv.AvailableTo("continue"); len(got) != 0 {
		t.Fatalf("AvailableTo(continue) = %#v; want none, continue is neither Automatic nor declared", got)
	}
}
