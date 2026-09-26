package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
)

// diskSnapshot records every path under root with its content or link
// target, so a test can prove a command changed nothing there.
func diskSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			target, linkErr := os.Readlink(path)
			snapshot[rel] = "link:" + target
			return linkErr
		case entry.IsDir():
			snapshot[rel] = "dir"
		default:
			data, readErr := os.ReadFile(path)
			sum := sha256.Sum256(data)
			snapshot[rel] = hex.EncodeToString(sum[:])
			return readErr
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return snapshot
}

func TestCLIDiffExitCodesAndReadOnly(t *testing.T) {
	resetRootCmdFlags()
	t.Cleanup(resetRootCmdFlags)
	home := isolateHome(t)
	root := t.TempDir()
	configFile, skillsDir, cacheDir, origin := filepath.Join(root, "skills.json"), filepath.Join(root, "skills"), filepath.Join(root, "cache"), filepath.Join(root, "origin")
	writeCLIGitSkill(t, origin, "sample")
	writeCLILocalSkill(t, root, "mine")
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	repo := cfg.Remote["owner/repo"]
	repo.Branch = cliRunGit(t, origin, "symbolic-ref", "--short", "HEAD")
	cfg.Remote["owner/repo"] = repo
	config.AddLocalSymlinkEntry(cfg, "mine", filepath.Join(root, "local", "mine"), "")
	cfg.Remote["owner/uncached"] = config.RemoteRepo{Type: "git", URL: origin, Branch: "elsewhere", Skills: map[string]string{"uncached": "sample"}}
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.NewCache("owner/repo", origin, repo.Branch, cacheDir).Refresh(false, "sample"); err != nil {
		t.Fatal(err)
	}
	paths := []string{"--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir}
	if out, err := runCLI(t, append([]string{"sync"}, paths...)...); exitCodeOf(err) != 1 {
		// The uncached Source keeps Sync at 1; sample is applied either way.
		t.Fatalf("sync: %v\n%s", err, out)
	}
	resetSubcommandFlags()

	stateDir := filepath.Join(home, ".local", "state")
	diff := func(args ...string) (string, int) {
		t.Helper()
		t.Cleanup(resetSubcommandFlags)
		scope, state := diskSnapshot(t, skillsDir), diskSnapshot(t, stateDir)
		configBefore, _ := os.ReadFile(configFile)
		out, err := runCLI(t, append(append([]string{"diff"}, args...), paths...)...)
		resetSubcommandFlags()
		configAfter, _ := os.ReadFile(configFile)
		if !reflect.DeepEqual(scope, diskSnapshot(t, skillsDir)) || !reflect.DeepEqual(state, diskSnapshot(t, stateDir)) || string(configBefore) != string(configAfter) {
			t.Fatalf("diff %v changed the Scope, its state, or its Config:\n%s", args, out)
		}
		return out, exitCodeOf(err)
	}

	// 0: the Scope copy is its Baseline, and the Cache has not moved.
	if out, code := diff("sample"); code != 0 || !strings.Contains(out, "Upstream") || !strings.Contains(out, "Local") {
		t.Fatalf("clean diff = %d:\n%s", code, out)
	}

	// 1: a local edit, shown as a unified diff.
	if err := os.WriteFile(filepath.Join(skillsDir, "sample", "SKILL.md"), []byte("# Sample\nmanual\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := diff("sample")
	if code != 1 || !strings.Contains(out, "+++ b/SKILL.md\n") || !strings.Contains(out, "+manual\n") {
		t.Fatalf("local edit diff = %d:\n%s", code, out)
	}
	if strings.Contains(out, "\033[") {
		t.Fatalf("output without a terminal must be plain:\n%q", out)
	}
	if out, code = diff("--stat", "sample"); code != 1 || !strings.Contains(out, "SKILL.md | +1 -0") || strings.Contains(out, "@@") {
		t.Fatalf("--stat = %d:\n%s", code, out)
	}

	// Offline by default: a new upstream commit is not seen until --fetch.
	if err := os.WriteFile(filepath.Join(origin, "sample", "SKILL.md"), []byte("# Sample\nupstream\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cliRunGit(t, origin, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-am", "upstream")
	if out, _ = diff("sample"); strings.Contains(out, "+upstream") {
		t.Fatalf("diff without --fetch reached the Source:\n%s", out)
	}
	if out, code = diff("--fetch", "sample"); code != 1 || !strings.Contains(out, "+upstream\n") || !strings.Contains(out, "+manual\n") {
		t.Fatalf("diff --fetch = %d:\n%s", code, out)
	}

	// 2: what diff cannot compare.
	for name, want := range map[string]string{"unknown": "not declared", "mine": "local Skill", "uncached": "Cache missing"} {
		if out, code := diff(name); code != 2 {
			t.Fatalf("diff %s = %d; want 2:\n%s", name, code, out)
		} else if _, err := runCLI(t, append([]string{"diff", name}, paths...)...); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("diff %s error = %v; want one mentioning %q", name, err, want)
		}
	}
}

func TestCLIDiffWithoutABaselineSaysWhy(t *testing.T) {
	resetRootCmdFlags()
	t.Cleanup(resetRootCmdFlags)
	isolateHome(t)
	root := t.TempDir()
	configFile, skillsDir, cacheDir, origin := filepath.Join(root, "skills.json"), filepath.Join(root, "skills"), filepath.Join(root, "cache"), filepath.Join(root, "origin")
	writeCLIGitSkill(t, origin, "sample")
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	if err := config.SaveConfig(cfg, configFile); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.NewCache("owner/repo", origin, "", cacheDir).Refresh(false, "sample"); err != nil {
		t.Fatal(err)
	}
	// A copy that arrived without Sync, so no Baseline was recorded.
	if err := os.MkdirAll(filepath.Join(skillsDir, "sample"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "sample", "SKILL.md"), []byte("# Mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "diff", "sample", "--config", configFile, "--skills-dir", skillsDir, "--cache-dir", cacheDir)
	if exitCodeOf(err) != 1 {
		t.Fatalf("diff = %v:\n%s", err, out)
	}
	for _, want := range []string{"No Baseline is recorded for sample", "-# Mine\n", "+# Sample\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Local") {
		t.Fatalf("a Skill without a Baseline has no Local section:\n%s", out)
	}
}

// On a terminal the patch is coloured by line kind; the file header stays
// bold and a removed line inside it is not mistaken for a deletion.
func TestPrintFileDiffsColoursAPatchByLineKind(t *testing.T) {
	saved := []string{colorBold, colorCyan, colorGreen, colorRed, colorReset}
	colorBold, colorCyan, colorGreen, colorRed, colorReset = "<b>", "<c>", "<g>", "<r>", "</>"
	t.Cleanup(func() {
		colorBold, colorCyan, colorGreen, colorRed, colorReset = saved[0], saved[1], saved[2], saved[3], saved[4]
	})
	var out strings.Builder
	printFileDiffs(&out, []engine.FileDiff{{Path: "SKILL.md", Patch: "diff --git a/SKILL.md b/SKILL.md\n--- a/SKILL.md\n+++ b/SKILL.md\n@@ -1,2 +1,2 @@\n # Demo\n-old\n+new\n"}}, false)
	want := "<b>diff --git a/SKILL.md b/SKILL.md</>\n<b>--- a/SKILL.md</>\n<b>+++ b/SKILL.md</>\n<c>@@ -1,2 +1,2 @@</>\n # Demo\n<r>-old</>\n<g>+new</>\n"
	if out.String() != want {
		t.Fatalf("coloured patch =\n%s\nwant\n%s", out.String(), want)
	}
}
