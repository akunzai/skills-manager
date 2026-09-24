package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

func TestParseSkillReplacesFromMD(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{"block form", "---\nname: new\nmetadata:\n  replaces: old\n---\n# New\n", []string{"old"}},
		{"several names", "---\nname: new\nmetadata:\n  author: someone\n  replaces: \"old, older\"\n---\n", []string{"old", "older"}},
		{"CRLF", "---\r\nname: new\r\nmetadata:\r\n  replaces: old\r\n---\r\n", []string{"old"}},
		{"replaces outside metadata", "---\nname: new\nreplaces: old\n---\n", nil},
		{"metadata ends before replaces", "---\nmetadata:\n  author: someone\nreplaces: old\n---\n", nil},
		{"replaces in the body", "---\nname: new\n---\nmetadata:\n  replaces: old\n", nil},
		{"no frontmatter", "# New\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "SKILL.md")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := ParseSkillReplacesFromMD(path); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("replaces = %#v; want %#v", got, tt.want)
			}
		})
	}
}

// renameFixture is a Scope that declared "old" from a Source and synced it.
type renameFixture struct {
	origin, url, branch             string
	configPath, skillsDir, cacheDir string
	cfg                             *config.Config
}

func newRenameFixture(t *testing.T) *renameFixture {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	f := &renameFixture{
		origin:     filepath.Join(root, "origin"),
		configPath: filepath.Join(root, "skills.json"),
		skillsDir:  filepath.Join(root, "skills"),
		cacheDir:   filepath.Join(root, "cache"),
	}
	f.write(t, "skills/old/SKILL.md", "---\nname: old\n---\n# Old\n")
	f.write(t, "skills/other/SKILL.md", "---\nname: other\n---\n# Other\n")
	mustGit(t, f.origin, "init")
	mustGit(t, f.origin, "config", "user.email", "test@example.com")
	mustGit(t, f.origin, "config", "user.name", "test")
	mustGit(t, f.origin, "config", "uploadpack.allowFilter", "true")
	f.commit(t, "init")
	f.url = localFileURL(f.origin)
	f.branch = mustGit(t, f.origin, "symbolic-ref", "--short", "HEAD")

	f.cfg = config.DefaultConfig()
	f.cfg.Remote["owner/repo"] = config.RemoteRepo{Type: "git", URL: f.url, Branch: f.branch, Skills: map[string]string{"old": "skills/old"}}
	f.cfg.Settings.Availability["old"] = config.AvailabilityOverride{Exclude: []string{"claude-code"}}
	if err := config.SaveConfig(f.cfg, f.configPath); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateRemoteSkills(f.cfg, nil, false, false, f.cacheDir, nil); err != nil {
		t.Fatal(err)
	}
	if report := f.sync(t, SyncDecision{}); !report.Converged() {
		t.Fatalf("initial sync did not converge: %#v", report)
	}
	return f
}

func (f *renameFixture) write(t *testing.T, path, content string) {
	t.Helper()
	file := filepath.Join(f.origin, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *renameFixture) commit(t *testing.T, message string) {
	t.Helper()
	mustGit(t, f.origin, "add", "-A")
	mustGit(t, f.origin, "commit", "-m", message)
}

// renameUpstream moves skills/old to skills/<name>, declaring it replaces old.
func (f *renameFixture) renameUpstream(t *testing.T, name, replaces string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(f.origin, "skills", "old")); err != nil {
		t.Fatal(err)
	}
	f.write(t, "skills/"+name+"/SKILL.md", "---\nname: "+name+"\nmetadata:\n  replaces: "+replaces+"\n---\n# New\n")
	f.commit(t, "rename")
}

func (f *renameFixture) update(t *testing.T) *UpdateResult {
	t.Helper()
	result, err := UpdateRemoteSkills(f.cfg, nil, false, false, f.cacheDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("update errors: %#v", result.Errors)
	}
	return result
}

func (f *renameFixture) plan(t *testing.T) *SyncPlan {
	t.Helper()
	cfg, err := config.LoadConfig(f.configPath)
	if err != nil {
		t.Fatal(err)
	}
	f.cfg = cfg
	plan, err := PlanSync(cfg, f.configPath, f.skillsDir, f.cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func (f *renameFixture) sync(t *testing.T, decision SyncDecision) *SyncReport {
	t.Helper()
	report, err := f.plan(t).Apply(decision, nil)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func (f *renameFixture) skill(t *testing.T, name string) SkillFreshness {
	t.Helper()
	snapshot, err := InspectFreshness(f.cfg, f.skillsDir, f.cacheDir, FreshnessOptions{ObserveScope: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, repository := range snapshot.Repositories {
		for _, skill := range repository.Skills {
			if skill.Name == name {
				return skill
			}
		}
	}
	t.Fatalf("%s not in the Freshness snapshot", name)
	return SkillFreshness{}
}

func (f *renameFixture) declared(t *testing.T) map[string]string {
	t.Helper()
	cfg, err := config.LoadConfig(f.configPath)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Remote["owner/repo"].Skills
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestSyncFollowsASkillRenamedUpstream(t *testing.T) {
	f := newRenameFixture(t)
	f.renameUpstream(t, "new", "old")

	result := f.update(t)
	want := []RenamedSkillInfo{{Source: "owner/repo", From: "old", To: "new", Subpath: "skills/new"}}
	if !reflect.DeepEqual(result.Renamed, want) {
		t.Fatalf("update renamed = %#v; want %#v", result.Renamed, want)
	}
	skill := f.skill(t, "old")
	if skill.Status != SkillRenamed || skill.RenamedTo != "new" || skill.RenamedSubpath != "skills/new" || skill.ScopeCopy != ScopeCopyClean {
		t.Fatalf("freshness = %#v", skill)
	}

	plan := f.plan(t)
	if pending := plan.Pending(SyncDecision{}); len(pending) != 1 || pending[0].Name != "old" {
		t.Fatalf("pending = %#v", pending)
	}
	if action, _ := plan.Items[0].Resolve(SyncDecision{}); action != SyncActionRename {
		t.Fatalf("action = %q; want rename", action)
	}
	report, err := plan.Apply(SyncDecision{}, nil)
	if err != nil || !report.Converged() {
		t.Fatalf("rename did not converge: err=%v report=%#v", err, report)
	}
	if !reflect.DeepEqual(report.Configured, []string{"new"}) {
		t.Fatalf("configured = %v", report.Configured)
	}

	cfg, err := config.LoadConfig(f.configPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := cfg.Remote["owner/repo"]
	if !reflect.DeepEqual(repo.Skills, map[string]string{"new": "skills/new"}) || repo.Branch != f.branch || repo.URL != f.url {
		t.Fatalf("Config Source = %#v", repo)
	}
	if _, ok := cfg.Settings.Availability["old"]; ok {
		t.Fatal("the old Availability override was kept")
	}
	if got := cfg.Settings.Availability["new"]; !reflect.DeepEqual(got.Exclude, []string{"claude-code"}) {
		t.Fatalf("Availability override not carried across: %#v", got)
	}
	if exists(filepath.Join(f.skillsDir, "old")) {
		t.Fatal("the old Skill directory was not removed")
	}
	got, err := os.ReadFile(filepath.Join(f.skillsDir, "new", "SKILL.md"))
	if err != nil || !strings.Contains(string(got), "replaces: old") {
		t.Fatalf("new Skill not Materialized: %q %v", got, err)
	}
	store, err := NewScopeStateStore(f.skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state.Skills["old"]; ok {
		t.Fatal("the old baseline was kept")
	}
	if _, ok := state.Skills["new"]; !ok {
		t.Fatal("no baseline recorded for the new Skill")
	}
	if plan := f.plan(t); !plan.Fresh(SyncDecision{}) {
		t.Fatalf("Scope not fresh after the rename: pending=%#v blocked=%#v", plan.Pending(SyncDecision{}), plan.Blocked(SyncDecision{}))
	}
}

// Another Scope's update already fetched the commit that renamed the Skill,
// so this Scope's update finds its Cache current and must still follow it.
func TestUpdateFollowsARenameInAnAlreadyCurrentCache(t *testing.T) {
	f := newRenameFixture(t)
	f.renameUpstream(t, "new", "old")
	other := config.DefaultConfig()
	other.Remote["owner/repo"] = config.RemoteRepo{Type: "git", URL: f.url, Branch: f.branch, Skills: map[string]string{"other": "skills/other"}}
	if _, err := UpdateRemoteSkills(other, nil, false, false, f.cacheDir, nil); err != nil {
		t.Fatal(err)
	}
	if skill := f.skill(t, "old"); skill.Status != SkillRemovedUpstream {
		t.Fatalf("before this Scope's update, status = %q; want %q", skill.Status, SkillRemovedUpstream)
	}
	result := f.update(t)
	if len(result.UpdatedRepos) != 0 || len(result.Renamed) != 1 {
		t.Fatalf("update = %#v", result)
	}
	if skill := f.skill(t, "old"); skill.Status != SkillRenamed {
		t.Fatalf("status = %q; want %q", skill.Status, SkillRenamed)
	}
}

func TestSyncRenameProtectsAnEditedOldCopy(t *testing.T) {
	f := newRenameFixture(t)
	if err := os.WriteFile(filepath.Join(f.skillsDir, "old", "SKILL.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.renameUpstream(t, "new", "old")
	f.update(t)
	if skill := f.skill(t, "old"); skill.ScopeCopy != ScopeCopyDrift {
		t.Fatalf("scope copy = %q; want %q", skill.ScopeCopy, ScopeCopyDrift)
	}

	report := f.sync(t, SyncDecision{})
	if report.Blocked != 1 {
		t.Fatalf("blocked = %d; want 1", report.Blocked)
	}
	if !reflect.DeepEqual(f.declared(t), map[string]string{"old": "skills/old"}) {
		t.Fatalf("a blocked rename changed Config: %#v", f.declared(t))
	}
	if got, _ := os.ReadFile(filepath.Join(f.skillsDir, "old", "SKILL.md")); string(got) != "edited\n" {
		t.Fatalf("the edited copy was touched: %q", got)
	}
	if exists(filepath.Join(f.skillsDir, "new")) {
		t.Fatal("a blocked rename Materialized the new Skill")
	}

	if report := f.sync(t, SyncDecision{Force: true}); !report.Converged() {
		t.Fatalf("--force did not lift the block: %#v", report)
	}
	if !reflect.DeepEqual(f.declared(t), map[string]string{"new": "skills/new"}) || exists(filepath.Join(f.skillsDir, "old")) {
		t.Fatalf("forced rename incomplete: %#v", f.declared(t))
	}
}

func TestSyncRenameToADeclaredSkillOnlyDropsTheOld(t *testing.T) {
	f := newRenameFixture(t)
	f.renameUpstream(t, "new", "old")
	f.update(t)
	cfg, err := config.LoadConfig(f.configPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := cfg.Remote["owner/repo"]
	repo.Skills["new"] = "skills/new"
	cfg.Remote["owner/repo"] = repo
	cfg.Settings.Availability["new"] = config.AvailabilityOverride{Include: []string{"codex"}}
	if err := config.SaveConfig(cfg, f.configPath); err != nil {
		t.Fatal(err)
	}

	if report := f.sync(t, SyncDecision{}); !report.Converged() {
		t.Fatalf("sync did not converge: %#v", report)
	}
	cfg, err = config.LoadConfig(f.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Remote["owner/repo"].Skills, map[string]string{"new": "skills/new"}) {
		t.Fatalf("declared = %#v", cfg.Remote["owner/repo"].Skills)
	}
	if got := cfg.Settings.Availability["new"]; !reflect.DeepEqual(got.Include, []string{"codex"}) || len(got.Exclude) != 0 {
		t.Fatalf("the declared Skill's own override was replaced: %#v", got)
	}
	if exists(filepath.Join(f.skillsDir, "old")) || !exists(filepath.Join(f.skillsDir, "new", "SKILL.md")) {
		t.Fatal("expected old removed and new present")
	}
}

func TestSyncRenameRefusesAnOccupiedTarget(t *testing.T) {
	f := newRenameFixture(t)
	f.renameUpstream(t, "new", "old")
	f.update(t)
	untracked := filepath.Join(f.skillsDir, "new", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(untracked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(untracked, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := f.plan(t)
	if _, block := plan.Items[0].Resolve(SyncDecision{Force: true}); block != SyncBlockRenameOccupied {
		t.Fatalf("block under --force = %q; want %q", block, SyncBlockRenameOccupied)
	}
	if report, err := plan.Apply(SyncDecision{Force: true}, nil); err != nil || report.Blocked != 1 {
		t.Fatalf("err=%v report=%#v", err, report)
	}
	if got, _ := os.ReadFile(untracked); string(got) != "mine\n" {
		t.Fatalf("untracked occupancy was overwritten: %q", got)
	}
	if !reflect.DeepEqual(f.declared(t), map[string]string{"old": "skills/old"}) {
		t.Fatalf("Config changed: %#v", f.declared(t))
	}
}

func TestSyncNamesRmForASkillRemovedUpstream(t *testing.T) {
	f := newRenameFixture(t)
	if err := os.RemoveAll(filepath.Join(f.origin, "skills", "old")); err != nil {
		t.Fatal(err)
	}
	f.commit(t, "remove")
	if result := f.update(t); len(result.Renamed) != 0 {
		t.Fatalf("renamed = %#v", result.Renamed)
	}
	if skill := f.skill(t, "old"); skill.Status != SkillRemovedUpstream {
		t.Fatalf("status = %q; want %q", skill.Status, SkillRemovedUpstream)
	}
	plan := f.plan(t)
	item := plan.Items[0]
	if item.Block != SyncBlockRemovedUpstream || !strings.Contains(item.BlockReason, "skills rm old") || strings.Contains(item.BlockReason, "skills update") {
		t.Fatalf("block = %q (%s)", item.Block, item.BlockReason)
	}
	snapshot, err := InspectFreshness(f.cfg, f.skillsDir, f.cacheDir, FreshnessOptions{ObserveScope: true})
	if err != nil {
		t.Fatal(err)
	}
	if kinds := snapshot.DispositionKinds(); !reflect.DeepEqual(kinds, []FreshnessDispositionKind{FreshnessInvestigate}) {
		t.Fatalf("dispositions = %v", kinds)
	}
}

func TestUpdateMatchesOneOfSeveralReplacedNames(t *testing.T) {
	f := newRenameFixture(t)
	f.renameUpstream(t, "new", "ancient, old")
	if result := f.update(t); len(result.Renamed) != 1 || result.Renamed[0].To != "new" {
		t.Fatalf("renamed = %#v", result.Renamed)
	}
	if report := f.sync(t, SyncDecision{}); !report.Converged() {
		t.Fatalf("sync did not converge: %#v", report)
	}
	if !reflect.DeepEqual(f.declared(t), map[string]string{"new": "skills/new"}) {
		t.Fatalf("declared = %#v", f.declared(t))
	}
}
