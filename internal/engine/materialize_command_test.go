package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

// printFileCommand is a shell command that prints path to stdout.
func printFileCommand(path string) string {
	if runtime.GOOS == "windows" {
		return "type " + path
	}
	return "cat " + path
}

func stdoutCommandFixture(t *testing.T, printed string) (cfg *config.Config, skillsDir, skillMd string) {
	t.Helper()
	root := t.TempDir()
	skillsDir = filepath.Join(root, ".agents", "skills")
	printedPath := filepath.Join(root, "printed.md")
	if err := os.WriteFile(printedPath, []byte(printed), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg = config.DefaultConfig()
	config.AddLocalCommandEntry(cfg, "tool", printFileCommand(printedPath), "", "Configured tool", config.CommandOutputStdout)
	return cfg, skillsDir, filepath.Join(skillsDir, "tool", "SKILL.md")
}

func TestSyncWritesAStdoutCommandAsSkillMd(t *testing.T) {
	cfg, skillsDir, skillMd := stdoutCommandFixture(t, "# Tool\n\nDoes things.\n")

	if _, err := applyPlan(t, cfg, skillsDir, "", SyncDecision{}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(skillMd)
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nname: tool\ndescription: \"Configured tool\"\n---\n\n# Tool\n\nDoes things.\n"
	if strings.ReplaceAll(string(got), "\r\n", "\n") != want {
		t.Fatalf("SKILL.md = %q, want %q", got, want)
	}
	if declared := readSkillFrontmatter(skillMd); declared.Name != "tool" || declared.Description != "Configured tool" {
		t.Fatalf("frontmatter = %#v", declared)
	}
}

func TestSyncLeavesSkillMdWhenAStdoutCommandFails(t *testing.T) {
	cfg, skillsDir, skillMd := stdoutCommandFixture(t, "")
	entry := cfg.Local["tool"]
	entry.Command = "exit 1"
	cfg.Local["tool"] = entry
	mustWriteScopeStateTestFile(t, skillMd, []byte("kept\n"))

	report, err := applyPlan(t, cfg, skillsDir, "", SyncDecision{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 1 {
		t.Fatalf("a failing command should fail its Skill; report = %#v", report.SyncTally)
	}
	if got, _ := os.ReadFile(skillMd); string(got) != "kept\n" {
		t.Fatalf("SKILL.md = %q, want it untouched", got)
	}
}

func TestMaterializeCommandOutputRefusesEmptyOutput(t *testing.T) {
	_, skillsDir, skillMd := stdoutCommandFixture(t, "")
	empty := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MaterializeCommandOutput(printFileCommand(empty), skillsDir, "tool", ""); err == nil {
		t.Fatal("empty output should be refused")
	}
	if _, err := os.Stat(skillMd); !os.IsNotExist(err) {
		t.Fatalf("SKILL.md should not exist: %v", err)
	}
}

func TestMaterializeCommandOutputReplacesALinkRatherThanWritingThroughIt(t *testing.T) {
	_, skillsDir, skillMd := stdoutCommandFixture(t, "")
	elsewhere := t.TempDir()
	mustWriteScopeStateTestFile(t, filepath.Join(elsewhere, "SKILL.md"), []byte("theirs\n"))
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(skillsDir, "tool")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	printed := filepath.Join(t.TempDir(), "printed.md")
	if err := os.WriteFile(printed, []byte("---\nname: tool\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := MaterializeCommandOutput(printFileCommand(printed), skillsDir, "tool", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(elsewhere, "SKILL.md")); string(got) != "theirs\n" {
		t.Fatalf("wrote through the link: %q", got)
	}
	if info, err := os.Lstat(filepath.Dir(skillMd)); err != nil || !info.IsDir() {
		t.Fatalf("skill should be a directory: %v", err)
	}
}
