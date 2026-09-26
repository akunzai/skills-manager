package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

// diffFixture is a Scope that declared "sample" from a partial-clone Source
// and synced it, so the Skill has a Baseline at the Source's first commit.
type diffFixture struct {
	root, origin, url, branch string
	skillsDir, cacheDir       string
	cfg                       *config.Config
	baselineCommit            string
}

func newDiffFixture(t *testing.T) *diffFixture {
	t.Helper()
	root := t.TempDir()
	f := &diffFixture{
		root:      root,
		origin:    filepath.Join(root, "origin"),
		skillsDir: filepath.Join(root, "skills"),
		cacheDir:  filepath.Join(root, "cache"),
	}
	f.writeOrigin(t, "skills/sample/SKILL.md", "# Sample\nfirst\n")
	f.writeOrigin(t, "skills/other/SKILL.md", "# Other\n")
	mustGit(t, f.origin, "init")
	mustGit(t, f.origin, "config", "user.email", "test@example.com")
	mustGit(t, f.origin, "config", "user.name", "test")
	mustGit(t, f.origin, "config", "uploadpack.allowFilter", "true")
	f.baselineCommit = f.commitOrigin(t, "init")
	f.url = localFileURL(f.origin)
	f.branch = mustGit(t, f.origin, "symbolic-ref", "--short", "HEAD")

	f.cfg = config.DefaultConfig()
	f.cfg.Remote["owner/repo"] = config.RemoteRepo{Type: "git", URL: f.url, Branch: f.branch, Skills: map[string]string{"sample": "skills/sample"}}
	f.update(t)
	if report, err := applyPlan(t, f.cfg, f.skillsDir, f.cacheDir, SyncDecision{}, nil); err != nil || !report.Summary().Converged() {
		t.Fatalf("initial sync did not converge: %#v %v", report, err)
	}
	return f
}

func (f *diffFixture) writeOrigin(t *testing.T, path, content string) {
	t.Helper()
	writeDiffFile(t, filepath.Join(f.origin, filepath.FromSlash(path)), content)
}

func (f *diffFixture) commitOrigin(t *testing.T, message string) string {
	t.Helper()
	mustGit(t, f.origin, "add", "-A")
	mustGit(t, f.origin, "commit", "-m", message)
	return mustGit(t, f.origin, "rev-parse", "HEAD")
}

// update refreshes the Cache the way 'skills update' does, without the Sync.
func (f *diffFixture) update(t *testing.T) {
	t.Helper()
	result, err := UpdateRemoteSkills(f.cfg, nil, false, false, f.cacheDir, nil)
	if err != nil || len(result.Errors) != 0 {
		t.Fatalf("update: %v %#v", err, result)
	}
}

// changeUpstream commits a change to the Skill and one outside it, then
// refreshes the Cache.
func (f *diffFixture) changeUpstream(t *testing.T) string {
	t.Helper()
	f.writeOrigin(t, "skills/sample/SKILL.md", "# Sample\nsecond\n")
	f.writeOrigin(t, "skills/sample/notes.md", "upstream notes\n")
	f.writeOrigin(t, "skills/other/SKILL.md", "# Other changed\n")
	head := f.commitOrigin(t, "change")
	f.update(t)
	return head
}

// changeLocally edits the Scope copy: one file modified, one added.
func (f *diffFixture) changeLocally(t *testing.T) {
	t.Helper()
	writeDiffFile(t, filepath.Join(f.skillsDir, "sample", "SKILL.md"), "# Sample\nfirst\nmine\n")
	writeDiffFile(t, filepath.Join(f.skillsDir, "sample", "local.md"), "local notes\n")
}

// offline makes the Source unreachable, so a diff that needed the network
// would fail.
func (f *diffFixture) offline(t *testing.T) {
	t.Helper()
	if err := os.Rename(f.origin, f.origin+".offline"); err != nil {
		t.Fatal(err)
	}
}

func (f *diffFixture) diff(t *testing.T) *SkillDiff {
	t.Helper()
	before := treeSnapshot(t, f.root)
	d, err := DiffSkill(f.cfg, "sample", f.skillsDir, f.cacheDir)
	if err != nil {
		t.Fatalf("DiffSkill: %v", err)
	}
	if after := treeSnapshot(t, f.root); !reflect.DeepEqual(before, after) {
		t.Fatalf("DiffSkill changed files on disk:\nbefore %#v\nafter  %#v", before, after)
	}
	return d
}

func writeDiffFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fileStats reduces a section to path -> "+added -removed".
func fileStats(files []FileDiff) map[string]string {
	stats := make(map[string]string)
	for _, file := range files {
		stats[file.Path] = fmt.Sprintf("+%d -%d", file.Added, file.Removed)
	}
	return stats
}

func TestDiffSkillShowsUpstreamChangesSinceTheBaselineOffline(t *testing.T) {
	t.Parallel()
	f := newDiffFixture(t)
	head := f.changeUpstream(t)
	f.offline(t)

	d := f.diff(t)
	if d.BaselineCommit != f.baselineCommit || d.CacheCommit != head {
		t.Fatalf("commits = %s → %s; want %s → %s", d.BaselineCommit, d.CacheCommit, f.baselineCommit, head)
	}
	want := map[string]string{"SKILL.md": "+1 -1", "notes.md": "+1 -0"}
	if got := fileStats(d.Upstream); !reflect.DeepEqual(got, want) {
		t.Fatalf("upstream files = %v; want %v (the other Skill's change must not appear)", got, want)
	}
	patch := d.Upstream[0].Patch
	for _, line := range []string{"--- a/SKILL.md", "+++ b/SKILL.md", "-first", "+second"} {
		if !strings.Contains(patch, line+"\n") {
			t.Fatalf("SKILL.md patch lacks %q:\n%s", line, patch)
		}
	}
	if len(d.Local) != 0 || d.Empty() {
		t.Fatalf("local = %#v, empty = %v; want only upstream changes", d.Local, d.Empty())
	}
}

func TestDiffSkillShowsLocalChangesAgainstTheBaseline(t *testing.T) {
	t.Parallel()
	f := newDiffFixture(t)
	f.changeLocally(t)
	f.offline(t)

	d := f.diff(t)
	if len(d.Upstream) != 0 {
		t.Fatalf("upstream = %#v; want none", d.Upstream)
	}
	want := map[string]string{"SKILL.md": "+1 -0", "local.md": "+1 -0"}
	if got := fileStats(d.Local); !reflect.DeepEqual(got, want) {
		t.Fatalf("local files = %v; want %v", got, want)
	}
	skill := d.Local[0].Patch
	for _, line := range []string{"diff --git a/SKILL.md b/SKILL.md", "--- a/SKILL.md", "+++ b/SKILL.md", "+mine"} {
		if !strings.Contains(skill, line+"\n") {
			t.Fatalf("SKILL.md patch lacks %q:\n%s", line, skill)
		}
	}
	if added := d.Local[1].Patch; !strings.Contains(added, "diff --git a/local.md b/local.md\nnew file") {
		t.Fatalf("added file header:\n%s", added)
	}
}

func TestDiffSkillShowsUpstreamAndLocalChangesApart(t *testing.T) {
	t.Parallel()
	f := newDiffFixture(t)
	f.changeLocally(t)
	f.changeUpstream(t)
	f.offline(t)

	d := f.diff(t)
	if got, want := fileStats(d.Upstream), map[string]string{"SKILL.md": "+1 -1", "notes.md": "+1 -0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("upstream files = %v; want %v", got, want)
	}
	if got, want := fileStats(d.Local), map[string]string{"SKILL.md": "+1 -0", "local.md": "+1 -0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("local files = %v; want %v", got, want)
	}
}

func TestDiffSkillWithoutABaselineComparesTheScopeCopyWithTheCache(t *testing.T) {
	t.Parallel()
	f := newDiffFixture(t)
	f.changeLocally(t)
	head := f.changeUpstream(t)
	if err := OpenBaselines(f.skillsDir).Forget("sample"); err != nil {
		t.Fatal(err)
	}
	f.offline(t)

	d := f.diff(t)
	if d.BaselineCommit != "" || d.NoBaseline != NoBaselineNotRecorded || d.CacheCommit != head {
		t.Fatalf("baseline = %q, reason = %q, cache = %q", d.BaselineCommit, d.NoBaseline, d.CacheCommit)
	}
	// Scope copy → Cache: what applying the Cache would do to the copy.
	want := map[string]string{"SKILL.md": "+1 -2", "local.md": "+0 -1", "notes.md": "+1 -0"}
	if got := fileStats(d.Upstream); !reflect.DeepEqual(got, want) {
		t.Fatalf("upstream files = %v; want %v", got, want)
	}
	if d.Local != nil {
		t.Fatalf("local = %#v; want no Local section without a Baseline", d.Local)
	}
}

func TestDiffSkillRejectsWhatItCannotCompare(t *testing.T) {
	t.Parallel()
	f := newDiffFixture(t)
	config.AddLocalSymlinkEntry(f.cfg, "mine", filepath.Join(f.root, "mine"), "")
	config.AddLocalCommandEntry(f.cfg, "tool", "true", "true", "")
	f.cfg.Remote["owner/uncached"] = config.RemoteRepo{Type: "git", URL: f.url, Branch: "other", Skills: map[string]string{"uncached": "skills/other"}}

	for name, want := range map[string]string{
		"missing":  "not declared",
		"mine":     "local",
		"tool":     "command",
		"uncached": "Cache",
	} {
		if _, err := DiffSkill(f.cfg, name, f.skillsDir, f.cacheDir); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("DiffSkill(%s) error = %v; want one mentioning %q", name, err, want)
		}
	}
}
