package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

// denyLinkCreation makes every symbolic link attempt fail with err, the way
// the operating system would. It is what makes the copy fallback — a branch
// that fires only on Windows — reachable on every platform.
func denyLinkCreation(t *testing.T, err error) {
	t.Helper()
	previous := CreateSymbolicLink
	CreateSymbolicLink = func(target, link string) error {
		return &os.LinkError{Op: "symlink", Old: target, New: link, Err: err}
	}
	t.Cleanup(func() { CreateSymbolicLink = previous })
}

// copyFallbackScope declares one remote Skill whose Cache is ready, and
// returns the Scope skills directory, the Cache, the origin repository, and
// the Availability path claude-code reads.
func copyFallbackScope(t *testing.T) (cfg *config.Config, skillsDir, cacheDir, origin, availabilityPath string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	skillsDir = filepath.Join(root, ".agents", "skills")
	cacheDir = filepath.Join(root, "cache")
	origin = filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	if _, err := EnsureGitRepo("owner/repo", origin, "", false, cacheDir); err != nil {
		t.Fatal(err)
	}
	cfg = config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	return cfg, skillsDir, cacheDir, origin, filepath.Join(root, ".claude", "skills", "sample")
}

func TestSyncAppliesAvailabilityByCopyingWhenTheLinkPrivilegeIsDenied(t *testing.T) {
	cfg, skillsDir, cacheDir, _, availabilityPath := copyFallbackScope(t)
	denyLinkCreation(t, ErrLinkPrivilegeNotHeld)

	var copied []SyncEvent
	report, err := applyPlan(t, cfg, skillsDir, cacheDir, SyncDecision{}, func(ev SyncEvent) {
		if ev.Kind == SyncAvailabilityCopied {
			copied = append(copied, ev)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 0 || report.Blocked != 0 {
		t.Fatalf("a copy is a working Availability, not a failure: failed=%d blocked=%d", report.Failed, report.Blocked)
	}
	if len(copied) != 1 || copied[0].Skill != "sample" || !reflect.DeepEqual(copied[0].Agents, []string{"claude-code"}) {
		t.Fatalf("copy events = %#v; want one naming sample and claude-code", copied)
	}
	if !isManagedSkillCopy(availabilityPath, "sample", skillsDir) {
		t.Fatalf("%s is not a managed copy", availabilityPath)
	}
	if _, err := os.Stat(filepath.Join(availabilityPath, "SKILL.md")); err != nil {
		t.Fatalf("the copy must hold the Skill: %v", err)
	}
}

// A path that is too long, or a target on a network volume, must not be
// silently turned into a copy that hides the problem.
func TestSyncFailsWhenALinkErrorIsNotTheMissingPrivilege(t *testing.T) {
	cfg, skillsDir, cacheDir, _, availabilityPath := copyFallbackScope(t)
	denyLinkCreation(t, syscall.ENAMETOOLONG)

	var failed []SyncEvent
	report, err := applyPlan(t, cfg, skillsDir, cacheDir, SyncDecision{}, func(ev SyncEvent) {
		if ev.Kind == SyncAvailabilityFailed || ev.Kind == SyncAvailabilityCopied {
			failed = append(failed, ev)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 1 {
		t.Fatalf("Failed = %d; want the link error reported as a failure", report.Failed)
	}
	if len(failed) != 1 || failed[0].Kind != SyncAvailabilityFailed {
		t.Fatalf("events = %#v; want one availability failure and no copy", failed)
	}
	if _, err := os.Lstat(availabilityPath); !os.IsNotExist(err) {
		t.Fatalf("a non-privilege link error must leave no copy behind: %v", err)
	}
}

func TestSyncLeavesAnUnchangedManagedCopyAlone(t *testing.T) {
	cfg, skillsDir, cacheDir, _, availabilityPath := copyFallbackScope(t)
	denyLinkCreation(t, ErrLinkPrivilegeNotHeld)
	if _, err := applyPlan(t, cfg, skillsDir, cacheDir, SyncDecision{}, nil); err != nil {
		t.Fatal(err)
	}

	// A file only this test wrote: a rebuild copies the Skill afresh and
	// takes it with it, so its survival is proof nothing was rewritten.
	probe := filepath.Join(availabilityPath, "probe.txt")
	mustWriteScopeStateTestFile(t, probe, []byte("untouched\n"))

	if _, err := applyPlan(t, cfg, skillsDir, cacheDir, SyncDecision{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(probe); err != nil {
		t.Fatalf("a routine Sync rewrote an unchanged copy: %v", err)
	}
}

func TestSyncRebuildsAManagedCopyWhenTheSkillChanged(t *testing.T) {
	cfg, skillsDir, cacheDir, origin, availabilityPath := copyFallbackScope(t)
	denyLinkCreation(t, ErrLinkPrivilegeNotHeld)
	if _, err := applyPlan(t, cfg, skillsDir, cacheDir, SyncDecision{}, nil); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(availabilityPath, "probe.txt")
	mustWriteScopeStateTestFile(t, probe, []byte("stale\n"))

	mustWriteScopeStateTestFile(t, filepath.Join(origin, "sample", "SKILL.md"), []byte("# Sample v2\n"))
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "v2"}} {
		if stdout, stderr, err := RunGit(origin, args...); err != nil {
			t.Fatalf("git %v: %v\n%s\n%s", args, err, stdout, stderr)
		}
	}
	if _, err := UpdateRemoteSkills(cfg, nil, false, false, cacheDir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := applyPlan(t, cfg, skillsDir, cacheDir, SyncDecision{}, nil); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(availabilityPath, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "# Sample v2\n" {
		t.Fatalf("copy content = %q; skipping work left an Agent on stale content", got)
	}
	if _, err := os.Stat(probe); !os.IsNotExist(err) {
		t.Fatalf("a changed Skill must be copied afresh: %v", err)
	}
}

// A copy made before digests were recorded has a one-line marker. It reads as
// unknown and is rebuilt once, after which it records a digest like any other.
func TestSyncRebuildsAManagedCopyWithNoRecordedDigestOnce(t *testing.T) {
	cfg, skillsDir, cacheDir, _, availabilityPath := copyFallbackScope(t)
	denyLinkCreation(t, ErrLinkPrivilegeNotHeld)
	if _, err := applyPlan(t, cfg, skillsDir, cacheDir, SyncDecision{}, nil); err != nil {
		t.Fatal(err)
	}
	source, _, ok := readManagedCopyMarker(availabilityPath)
	if !ok {
		t.Fatalf("no managed copy marker in %s", availabilityPath)
	}
	if err := os.WriteFile(filepath.Join(availabilityPath, managedCopyMarker), []byte(source+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(availabilityPath, "probe.txt")
	mustWriteScopeStateTestFile(t, probe, []byte("stale\n"))

	if _, err := applyPlan(t, cfg, skillsDir, cacheDir, SyncDecision{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(probe); !os.IsNotExist(err) {
		t.Fatalf("an unknown baseline must be rebuilt: %v", err)
	}
	if _, digest, _ := readManagedCopyMarker(availabilityPath); digest == "" {
		t.Fatal("the rebuilt copy recorded no digest, so it would rebuild forever")
	}

	mustWriteScopeStateTestFile(t, probe, []byte("kept\n"))
	if _, err := applyPlan(t, cfg, skillsDir, cacheDir, SyncDecision{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(probe); err != nil {
		t.Fatalf("the rebuild must happen once, not on every Sync: %v", err)
	}
}

// Copies must not be permanent: turning Developer Mode on has to be enough to
// get links back, without the user having to delete anything by hand.
func TestSyncReplacesAManagedCopyWithALinkOnceThePrivilegeIsGranted(t *testing.T) {
	cfg, skillsDir, cacheDir, _, availabilityPath := copyFallbackScope(t)
	restore := CreateSymbolicLink
	denyLinkCreation(t, ErrLinkPrivilegeNotHeld)
	if _, err := applyPlan(t, cfg, skillsDir, cacheDir, SyncDecision{}, nil); err != nil {
		t.Fatal(err)
	}
	if !isManagedSkillCopy(availabilityPath, "sample", skillsDir) {
		t.Fatalf("%s is not a managed copy", availabilityPath)
	}

	CreateSymbolicLink = restore
	var copied []SyncEvent
	if _, err := applyPlan(t, cfg, skillsDir, cacheDir, SyncDecision{}, func(ev SyncEvent) {
		if ev.Kind == SyncAvailabilityCopied {
			copied = append(copied, ev)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !isManagedSkillLink(availabilityPath, "sample", skillsDir) {
		t.Fatalf("%s is still not a link; earlier copies are permanent", availabilityPath)
	}
	if len(copied) != 0 {
		t.Fatalf("Sync still reported copying after switching to links: %#v", copied)
	}
}

// The link attempt that makes the above possible must not cost the copy when
// the privilege is still missing.
func TestSyncKeepsAManagedCopyWhenThePrivilegeIsStillDenied(t *testing.T) {
	cfg, skillsDir, cacheDir, _, availabilityPath := copyFallbackScope(t)
	denyLinkCreation(t, ErrLinkPrivilegeNotHeld)
	if _, err := applyPlan(t, cfg, skillsDir, cacheDir, SyncDecision{}, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := applyPlan(t, cfg, skillsDir, cacheDir, SyncDecision{}, nil); err != nil {
		t.Fatal(err)
	}
	if !isManagedSkillCopy(availabilityPath, "sample", skillsDir) {
		t.Fatalf("a failed relink cost the working copy at %s", availabilityPath)
	}
	entries, err := os.ReadDir(filepath.Dir(availabilityPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".skills-manager-link-") {
			t.Fatalf("the relink attempt left %s behind", entry.Name())
		}
	}
}

// A Skill declared from a local Source is a symlink on the skills directory.
// Copying it as given would reproduce that link instead of reading through it,
// digest an empty tree, and write the marker into the user's own Source
// directory. Reachable whenever Availability is applied to a master that was
// linked while the privilege was held — an agents or config policy change
// after Developer Mode was turned off — so this drives Apply directly rather
// than a Sync, which re-Materializes the master first.
func TestAvailabilityCopiesThroughASymlinkedMasterSkill(t *testing.T) {
	root := t.TempDir()
	skillsDir := filepath.Join(root, ".agents", "skills")
	source := filepath.Join(root, "my-skill")
	mustWriteScopeStateTestFile(t, filepath.Join(source, "SKILL.md"), []byte("# Local\n"))
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	master := filepath.Join(skillsDir, "local")
	if err := os.Symlink(source, master); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	cfg := config.DefaultConfig()
	config.AddLocalSymlinkEntry(cfg, "local", source, "")
	availability := NewAvailability(cfg, skillsDir)
	availabilityPath := filepath.Join(root, ".claude", "skills", "local")

	denyLinkCreation(t, ErrLinkPrivilegeNotHeld)
	copied, err := availability.Apply("local")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(copied, []string{"claude-code"}) {
		t.Fatalf("Copied = %#v; want claude-code — the path is a copy or it is unmanaged", copied)
	}
	if !isManagedSkillCopy(availabilityPath, "local", skillsDir) {
		t.Fatalf("%s is not a managed copy, so the next apply would refuse it as unmanaged", availabilityPath)
	}
	if got, err := os.ReadFile(filepath.Join(availabilityPath, "SKILL.md")); err != nil || string(got) != "# Local\n" {
		t.Fatalf("copy content = %q (%v); the link was reproduced instead of read through", got, err)
	}
	if _, err := os.Lstat(filepath.Join(source, managedCopyMarker)); !os.IsNotExist(err) {
		t.Fatalf("a marker was written into the user's own Source directory: %v", err)
	}

	// The recorded digest has to be of real content, or a changed Skill could
	// never mismatch it and the copy would never be rebuilt.
	_, digest, _ := readManagedCopyMarker(availabilityPath)
	empty, err := DigestSkillTree(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" || digest == empty {
		t.Fatalf("recorded digest %q is the digest of nothing", digest)
	}
	mustWriteScopeStateTestFile(t, filepath.Join(source, "SKILL.md"), []byte("# Local v2\n"))
	if _, err := availability.Apply("local"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(availabilityPath, "SKILL.md")); string(got) != "# Local v2\n" {
		t.Fatalf("copy content = %q; a changed Skill must be rebuilt", got)
	}
}
