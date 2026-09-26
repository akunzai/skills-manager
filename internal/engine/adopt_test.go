package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// adoptFixture is a Project Scope whose skills directory holds Untracked
// Skills, beside a local origin repository an installer lock file can name.
type adoptFixture struct {
	root, skillsDir, cacheDir, configPath, lockPath, origin string
}

func newAdoptFixture(t *testing.T) adoptFixture {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	f := adoptFixture{
		root:       root,
		skillsDir:  filepath.Join(root, ".agents", "skills"),
		cacheDir:   filepath.Join(root, "cache"),
		configPath: filepath.Join(root, ".agents", "skills.json"),
		lockPath:   filepath.Join(root, "skills-lock.json"),
		origin:     filepath.Join(root, "origin"),
	}
	writeLocalGitSkill(t, f.origin, "sample")
	return f
}

// untracked writes an Untracked Skill directory with one SKILL.md.
func (f adoptFixture) untracked(t *testing.T, name, content string) string {
	t.Helper()
	dir := filepath.Join(f.skillsDir, name)
	mustWriteScopeStateTestFile(t, filepath.Join(dir, "SKILL.md"), []byte(content))
	return dir
}

// lock writes an installer lock file recording each Skill from the fixture's
// origin, the way the installer writes one: unknown fields included.
func (f adoptFixture) lock(t *testing.T, skillPaths map[string]string) {
	t.Helper()
	skills := map[string]any{}
	for name, skillPath := range skillPaths {
		skills[name] = map[string]any{
			"source":       "owner/repo",
			"sourceType":   "github",
			"sourceUrl":    f.origin,
			"skillPath":    skillPath,
			"computedHash": "ignored",
		}
	}
	data, err := json.Marshal(map[string]any{"version": 1, "skills": skills, "extra": true})
	if err != nil {
		t.Fatal(err)
	}
	mustWriteScopeStateTestFile(t, f.lockPath, data)
}

func (f adoptFixture) scope() models.Scope {
	return models.Scope{ConfigPath: f.configPath, SkillsDir: f.skillsDir, CacheDir: f.cacheDir, IsProject: true}
}

func (f adoptFixture) adopt(t *testing.T, cfg *config.Config, names ...string) AdoptResult {
	t.Helper()
	plan, err := BuildAdoptPlan(cfg, f.scope(), AdoptOptions{LockPath: f.lockPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) > 0 {
		plan = plan.Select(names)
	}
	return ApplyAdoptPlan(plan, cfg, f.scope())
}

// syncSummary is what a Sync dry run would report for the Scope right now.
func (f adoptFixture) syncSummary(t *testing.T) SyncSummary {
	t.Helper()
	cfg, err := config.LoadConfig(f.configPath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanSync(cfg, f.configPath, f.skillsDir, f.cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	return plan.Summary(SyncDecision{})
}

func (f adoptFixture) untrackedNow(t *testing.T) []string {
	t.Helper()
	cfg, err := config.LoadConfig(f.configPath)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := LoadInventory(cfg, f.skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	return inv.Untracked()
}

func TestAdoptDeclaresALockRecordedIdenticalCopyWithItsBaseline(t *testing.T) {
	f := newAdoptFixture(t)
	f.untracked(t, "sample", "# Sample\n")
	f.lock(t, map[string]string{"sample": "sample/SKILL.md"})
	cfg := config.DefaultConfig()

	result := f.adopt(t, cfg)

	if result.Blocked != 0 || result.Failed != 0 || len(result.Skills) != 1 {
		t.Fatalf("result = %#v; want one Skill adopted", result)
	}
	got := result.Skills[0]
	if got.Outcome != SyncDone || !got.Baseline || !got.Recorded || got.Action != AdoptDeclareRemote {
		t.Fatalf("adopted = %#v; want a remote declaration with its Baseline", got)
	}
	saved, err := config.LoadConfig(f.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if subpath := saved.Remote["owner/repo"].Skills["sample"]; subpath != "sample" {
		t.Fatalf("declared subpath = %q; want sample (from sample/SKILL.md)", subpath)
	}
	if _, ok := OpenBaselines(f.skillsDir).Applied("sample"); !ok {
		t.Fatal("no Baseline recorded for an identical copy")
	}
	if summary := f.syncSummary(t); !summary.Converged() {
		t.Fatalf("sync --dry-run after adopt = %#v; want converged", summary)
	}
	if untracked := f.untrackedNow(t); len(untracked) != 0 {
		t.Fatalf("still Untracked after adopt: %v", untracked)
	}
	if info, err := os.Lstat(filepath.Join(f.skillsDir, "sample")); err != nil || !info.IsDir() {
		t.Fatalf("the adopted copy must stay a real directory: %v", err)
	}
}

func TestAdoptDeclaresALockRecordedModifiedCopyWithoutABaseline(t *testing.T) {
	f := newAdoptFixture(t)
	f.untracked(t, "sample", "# Sample, edited here\n")
	f.lock(t, map[string]string{"sample": "sample"})
	cfg := config.DefaultConfig()

	result := f.adopt(t, cfg)

	if len(result.Skills) != 1 || result.Blocked != 1 || result.Failed != 0 {
		t.Fatalf("result = %#v; want the Skill declared but blocked", result)
	}
	if got := result.Skills[0]; got.Baseline || got.Reason == "" {
		t.Fatalf("adopted = %#v; want no Baseline and a reason", got)
	}
	saved, err := config.LoadConfig(f.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.Remote["owner/repo"].Skills["sample"]; !ok {
		t.Fatal("a modified copy must still be declared")
	}
	if _, ok := OpenBaselines(f.skillsDir).Applied("sample"); ok {
		t.Fatal("a modified copy must not get a Baseline")
	}
	if summary := f.syncSummary(t); summary.Blocked != 1 || !summary.Forceable {
		t.Fatalf("sync after adopting a modified copy = %#v; want it blocked and askable", summary)
	}
	data, err := os.ReadFile(filepath.Join(f.skillsDir, "sample", "SKILL.md"))
	if err != nil || string(data) != "# Sample, edited here\n" {
		t.Fatalf("the modified copy was changed: %q, %v", data, err)
	}
}

func TestAdoptMovesAnUnknownOriginBesideTheSkillsDirectory(t *testing.T) {
	f := newAdoptFixture(t)
	f.untracked(t, "mine", "# Mine\n")
	cfg := config.DefaultConfig()

	result := f.adopt(t, cfg)

	if result.Blocked != 0 || result.Failed != 0 || len(result.Adopted()) != 1 {
		t.Fatalf("result = %#v; want one Skill adopted", result)
	}
	moved := filepath.Join(f.root, ".agents", "skills-local", "mine")
	if got := result.Skills[0]; got.Action != AdoptMoveLocal || got.MoveTo != moved {
		t.Fatalf("adopted = %#v; want a move to %s", got, moved)
	}
	if data, err := os.ReadFile(filepath.Join(moved, "SKILL.md")); err != nil || string(data) != "# Mine\n" {
		t.Fatalf("moved Skill content = %q, %v", data, err)
	}
	link := filepath.Join(f.skillsDir, "mine")
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("skills directory entry must now be a symlink: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(link); err != nil || resolved != mustEvalSymlinks(t, moved) {
		t.Fatalf("symlink resolves to %q (%v); want %s", resolved, err, moved)
	}
	saved, err := config.LoadConfig(f.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if entry := saved.Local["mine"]; entry.Type != "symlink" || entry.Source != ".agents/skills-local/mine" {
		t.Fatalf("declared = %#v; want a project-relative local symlink Source", entry)
	}
	if summary := f.syncSummary(t); !summary.Converged() {
		t.Fatalf("sync --dry-run after adopt = %#v; want converged", summary)
	}
	if untracked := f.untrackedNow(t); len(untracked) != 0 {
		t.Fatalf("still Untracked after adopt: %v", untracked)
	}
}

func TestAdoptMovesAGitCheckoutWithItsGitDirectoryEvenWhenLockRecorded(t *testing.T) {
	f := newAdoptFixture(t)
	dir := f.untracked(t, "sample", "# Sample\n")
	for _, args := range [][]string{{"init"}, {"config", "user.email", "test@example.com"}, {"config", "user.name", "test"}, {"add", "."}, {"commit", "-m", "mine"}} {
		if stdout, stderr, err := runGit(dir, args...); err != nil {
			t.Fatalf("git %v: %v\n%s%s", args, err, stdout, stderr)
		}
	}
	f.lock(t, map[string]string{"sample": "sample/SKILL.md"})
	cfg := config.DefaultConfig()

	result := f.adopt(t, cfg)

	if result.Blocked != 0 || result.Failed != 0 || len(result.Skills) != 1 {
		t.Fatalf("result = %#v; want one Skill adopted", result)
	}
	got := result.Skills[0]
	if got.Action != AdoptMoveLocal || !got.GitCheckout || !got.Recorded {
		t.Fatalf("adopted = %#v; want a recorded git checkout moved", got)
	}
	if stdout, stderr, err := runGit(got.MoveTo, "log", "--format=%s"); err != nil || stdout != "mine" {
		t.Fatalf("moved checkout history = %q (%v, %s); want its own commit", stdout, err, stderr)
	}
	if _, err := os.Stat(filepath.Join(f.cacheDir)); !os.IsNotExist(err) {
		t.Fatalf("a git checkout must not be fetched from its lock record: %v", err)
	}
}

func TestAdoptRefusesAMoveOntoAnExistingTarget(t *testing.T) {
	f := newAdoptFixture(t)
	f.untracked(t, "mine", "# Mine\n")
	occupied := filepath.Join(f.root, ".agents", "skills-local", "mine")
	mustWriteScopeStateTestFile(t, filepath.Join(occupied, "keep.txt"), []byte("already here\n"))
	cfg := config.DefaultConfig()

	plan, err := BuildAdoptPlan(cfg, f.scope(), AdoptOptions{LockPath: f.lockPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Block == "" {
		t.Fatalf("plan = %#v; want the collision shown before anything moves", plan)
	}
	result := ApplyAdoptPlan(plan, cfg, f.scope())

	if result.Blocked != 1 || len(result.Adopted()) != 0 {
		t.Fatalf("result = %#v; want the Skill left blocked", result)
	}
	if !isRealDir(filepath.Join(f.skillsDir, "mine")) {
		t.Fatal("a refused move must leave the directory where it was")
	}
	if data, err := os.ReadFile(filepath.Join(occupied, "keep.txt")); err != nil || string(data) != "already here\n" {
		t.Fatalf("the existing target was touched: %q, %v", data, err)
	}
	if _, err := os.Stat(f.configPath); !os.IsNotExist(err) {
		t.Fatalf("nothing adopted, so Config must not be written: %v", err)
	}
}

func TestAdoptPlanWritesNothing(t *testing.T) {
	f := newAdoptFixture(t)
	f.untracked(t, "sample", "# Sample\n")
	f.untracked(t, "mine", "# Mine\n")
	f.lock(t, map[string]string{"sample": "sample/SKILL.md"})
	before := treeSnapshot(t, f.root)

	plan, err := BuildAdoptPlan(config.DefaultConfig(), f.scope(), AdoptOptions{LockPath: f.lockPath})
	if err != nil {
		t.Fatal(err)
	}

	if got := plan.Names(); len(got) != 2 {
		t.Fatalf("plan offers %v; want both Untracked Skills", got)
	}
	if after := treeSnapshot(t, f.root); !reflect.DeepEqual(before, after) {
		t.Fatalf("planning changed the Scope:\nbefore %#v\nafter  %#v", before, after)
	}
}

func TestAdoptSelectLeavesUnselectedSkillsAlone(t *testing.T) {
	f := newAdoptFixture(t)
	f.untracked(t, "mine", "# Mine\n")
	f.untracked(t, "other", "# Other\n")

	result := f.adopt(t, config.DefaultConfig(), "mine")

	if len(result.Skills) != 1 || result.Skills[0].Name != "mine" {
		t.Fatalf("result = %#v; want only the selected Skill", result)
	}
	if !isRealDir(filepath.Join(f.skillsDir, "other")) {
		t.Fatal("an unselected Skill must stay where it is")
	}
}

func TestReadInstallerLockNormalisesSkillPathAndIgnoresNonGitSources(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), ".skill-lock.json")
	mustWriteScopeStateTestFile(t, lockPath, []byte(`{
  "version": 3,
  "skills": {
    "pdf": {"source": "owner/repo", "sourceType": "github", "sourceUrl": "https://github.com/owner/repo.git", "ref": "v2", "skillPath": "skills/pdf/SKILL.md", "skillFolderHash": "abc", "installedAt": "x"},
    "dir": {"source": "https://gitlab.com/group/project", "sourceType": "gitlab", "skillPath": "skills/dir/"},
    "root": {"source": "owner/root", "sourceType": "github", "skillPath": "SKILL.md"},
    "escape": {"source": "owner/repo", "sourceType": "github", "skillPath": "../x/SKILL.md"},
    "local": {"source": "/tmp/somewhere", "sourceType": "local"}
  },
  "dismissed": {"findSkillsPrompt": true}
}`))

	records, err := ReadInstallerLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]InstallerLockRecord{
		"pdf":    {Source: "owner/repo", RepoType: "github", URL: "https://github.com/owner/repo.git", Branch: "v2", Subpath: "skills/pdf"},
		"dir":    {Source: "gitlab.com/group/project", RepoType: "gitlab", URL: "https://gitlab.com/group/project.git", Subpath: "skills/dir"},
		"root":   {Source: "owner/root", RepoType: "github", URL: "https://github.com/owner/root.git", Subpath: "."},
		"escape": {Source: "owner/repo", RepoType: "github", URL: "https://github.com/owner/repo.git"},
		"local":  {},
	}
	if !reflect.DeepEqual(records, want) {
		t.Fatalf("records = %#v\nwant %#v", records, want)
	}
	if records["pdf"].StoredURL() != "" || records["dir"].StoredURL() == "" {
		t.Fatalf("stored URLs: pdf %q, dir %q", records["pdf"].StoredURL(), records["dir"].StoredURL())
	}
}

func TestInstallerLockPathFollowsTheScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AGENTS_HOME", "")
	project := filepath.Join(t.TempDir(), "demo")

	if got, want := InstallerLockPath(filepath.Join(project, ".agents", "skills")), filepath.Join(project, "skills-lock.json"); got != want {
		t.Errorf("Project lock = %s; want %s", got, want)
	}
	t.Setenv("XDG_STATE_HOME", "")
	if got, want := InstallerLockPath(models.DefaultSkillsDir()), filepath.Join(home, ".agents", ".skill-lock.json"); got != want {
		t.Errorf("Global lock = %s; want %s", got, want)
	}
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	if got, want := InstallerLockPath(models.DefaultSkillsDir()), filepath.Join(state, "skills", ".skill-lock.json"); got != want {
		t.Errorf("Global lock with XDG_STATE_HOME = %s; want %s", got, want)
	}
}

func mustEvalSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
