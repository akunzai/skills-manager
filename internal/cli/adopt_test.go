package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adoptCLIScope is a scratch Scope: a custom --skills-dir is Project-scoped,
// so its installer lock file is skills-lock.json in root and moved Skills go
// to root/skills-local.
func adoptCLIScope(t *testing.T) (root string, args []string) {
	t.Helper()
	resetRootCmdFlags()
	t.Cleanup(resetRootCmdFlags)
	isolateHome(t)
	root = t.TempDir()
	args = []string{"--config", filepath.Join(root, "skills.json"), "--skills-dir", filepath.Join(root, "skills"), "--cache-dir", filepath.Join(root, "cache")}
	return root, args
}

func writeUntrackedCLISkill(t *testing.T, root, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeCLIInstallerLock(t *testing.T, root string, entries map[string]map[string]string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"version": 1, "skills": entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills-lock.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func runAdoptCLI(t *testing.T, scopeArgs []string, args ...string) (string, error) {
	t.Helper()
	resetSubcommandFlags()
	return runCLI(t, append(args, scopeArgs...)...)
}

func TestCLIAdoptExitCodes(t *testing.T) {
	root, scope := adoptCLIScope(t)
	origin := filepath.Join(root, "origin")
	writeCLIGitSkill(t, origin, "sample")
	mine := writeUntrackedCLISkill(t, root, "mine", "# Mine\n")
	edited := writeUntrackedCLISkill(t, root, "sample", "# Sample, edited here\n")
	writeCLIInstallerLock(t, root, map[string]map[string]string{
		"sample": {"source": "owner/repo", "sourceType": "github", "sourceUrl": origin, "skillPath": "sample/SKILL.md"},
	})
	lockBefore, err := os.ReadFile(filepath.Join(root, "skills-lock.json"))
	if err != nil {
		t.Fatal(err)
	}

	// 2: an unknown name is refused before anything happens.
	out, err := runAdoptCLI(t, scope, "adopt", "nope", "-y")
	if exitCodeOf(err) != 2 || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("unknown name should exit 2 naming it, got err=%v:\n%s", err, out)
	}

	// 0: a dry run previews every move and writes nothing.
	out, err = runAdoptCLI(t, scope, "adopt", "--dry-run")
	if err != nil {
		t.Fatalf("dry run should exit 0: %v\n%s", err, out)
	}
	if !strings.Contains(out, "mine: move to ") || !strings.Contains(out, filepath.Join("skills-local", "mine")) || !strings.Contains(out, "sample: declare from owner/repo (sample)") {
		t.Fatalf("dry run must preview each Skill's move or Source:\n%s", out)
	}
	if !strings.Contains(out, "installer lock file also records sample") {
		t.Fatalf("dry run must warn about the other installer:\n%s", out)
	}
	if !isRealDirPath(mine) {
		t.Fatal("dry run moved a Skill")
	}

	// 2: without a terminal, adopt refuses to move anything unconfirmed.
	out, err = runAdoptCLI(t, scope, "adopt", "mine")
	if exitCodeOf(err) != 2 || !strings.Contains(out, "mine: move to ") {
		t.Fatalf("unconfirmed adopt without a terminal should preview and exit 2, got err=%v:\n%s", err, out)
	}
	if !isRealDirPath(mine) {
		t.Fatal("a refused adopt moved a Skill")
	}

	// 1: --yes without names or --all selects nothing; every Skill is left.
	out, err = runAdoptCLI(t, scope, "adopt", "-y")
	if exitCodeOf(err) != 1 || !strings.Contains(out, "Skipped 2 untracked skills") || !strings.Contains(out, "--all") {
		t.Fatalf("--yes alone should skip every Skill and exit 1, got err=%v:\n%s", err, out)
	}
	if !isRealDirPath(mine) {
		t.Fatal("--yes alone moved a Skill")
	}

	// 0: an unknown origin is moved and declared; the Scope then matches.
	out, err = runAdoptCLI(t, scope, "adopt", "mine", "-y")
	if err != nil {
		t.Fatalf("adopting an unknown origin should exit 0: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Adopted 1 skill") {
		t.Fatalf("adopt must say what it adopted:\n%s", out)
	}
	if !isSymlink(mine) || !isRealDirPath(filepath.Join(root, "skills-local", "mine")) {
		t.Fatal("mine must be moved to skills-local and linked back")
	}

	// 1: a lock-recorded copy that differs is declared, and Sync blocks it.
	out, err = runAdoptCLI(t, scope, "adopt", "--all", "-y")
	if exitCodeOf(err) != 1 {
		t.Fatalf("a modified lock-recorded copy should exit 1, got err=%v:\n%s", err, out)
	}
	if !strings.Contains(out, "Declared sample from owner/repo") || !strings.Contains(out, "next Sync asks") {
		t.Fatalf("adopt must say the copy was declared without a Baseline:\n%s", out)
	}
	if !strings.Contains(out, "installer lock file also records sample") {
		t.Fatalf("adopt must warn about the other installer:\n%s", out)
	}
	if !isRealDirPath(edited) {
		t.Fatal("a lock-recorded copy must stay in place")
	}
	out, err = runAdoptCLI(t, scope, "sync", "--dry-run")
	if exitCodeOf(err) != 1 || !strings.Contains(out, "--force") {
		t.Fatalf("Sync after adopting a modified copy should block and ask, got err=%v:\n%s", err, out)
	}
	if lockAfter, err := os.ReadFile(filepath.Join(root, "skills-lock.json")); err != nil || string(lockAfter) != string(lockBefore) {
		t.Fatalf("adopt must never write the installer lock file: %v", err)
	}

	// 0: nothing Untracked is left.
	out, err = runAdoptCLI(t, scope, "adopt", "--all", "-y")
	if err != nil || !strings.Contains(out, "Nothing to adopt.") {
		t.Fatalf("nothing left should exit 0, got err=%v:\n%s", err, out)
	}
}

func TestCLIAdoptCleanScopeSyncsAndDoctorsClean(t *testing.T) {
	root, scope := adoptCLIScope(t)
	origin := filepath.Join(root, "origin")
	writeCLIGitSkill(t, origin, "sample")
	writeUntrackedCLISkill(t, root, "sample", "# Sample\n")
	writeUntrackedCLISkill(t, root, "mine", "# Mine\n")
	writeCLIInstallerLock(t, root, map[string]map[string]string{
		"sample": {"source": "owner/repo", "sourceType": "github", "sourceUrl": origin, "skillPath": "sample"},
	})

	out, err := runAdoptCLI(t, scope, "adopt", "--all", "-y")
	if err != nil {
		t.Fatalf("clean adoption should exit 0: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Adopted sample from owner/repo (sample).") {
		t.Fatalf("adopt must name the declared Source:\n%s", out)
	}
	if out, err := runAdoptCLI(t, scope, "sync", "--dry-run"); err != nil || strings.Contains(out, "ntracked") {
		t.Fatalf("sync --dry-run after a clean adoption should exit 0: %v\n%s", err, out)
	}
	if out, err := runAdoptCLI(t, scope, "doctor"); err != nil {
		t.Fatalf("doctor after a clean adoption should exit 0: %v\n%s", err, out)
	}
}

func TestCLIAdoptFetchFailureExitsTwo(t *testing.T) {
	root, scope := adoptCLIScope(t)
	skill := writeUntrackedCLISkill(t, root, "sample", "# Sample\n")
	writeCLIInstallerLock(t, root, map[string]map[string]string{
		"sample": {"source": "owner/repo", "sourceType": "github", "sourceUrl": filepath.Join(root, "missing-origin"), "ref": "main", "skillPath": "sample"},
	})

	out, err := runAdoptCLI(t, scope, "adopt", "sample", "-y")
	if exitCodeOf(err) != 2 || !strings.Contains(out, "Failed to adopt sample") {
		t.Fatalf("a Source that cannot be fetched should exit 2, got err=%v:\n%s", err, out)
	}
	if !isRealDirPath(skill) {
		t.Fatal("a failed adopt must leave the copy in place")
	}
	if _, err := os.Stat(filepath.Join(root, "skills.json")); !os.IsNotExist(err) {
		t.Fatalf("a failed adopt must not declare the Skill: %v", err)
	}
}

func TestCLIAdoptRejectsAllWithNames(t *testing.T) {
	_, scope := adoptCLIScope(t)
	out, err := runAdoptCLI(t, scope, "adopt", "--all", "mine")
	if exitCodeOf(err) != 2 || !strings.Contains(err.Error(), "--all") {
		t.Fatalf("--all with names should be refused, got err=%v:\n%s", err, out)
	}
}

func isRealDirPath(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}
