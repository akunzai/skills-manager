package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/spf13/cobra"
)

// fakeInstallSource is a no-op installSource used to exercise
// resolveSkillsToInstall's selection logic without touching git, the
// filesystem's symlink/copy primitives, or a terminal.
type fakeInstallSource struct {
	dir         string
	allowRename bool
}

func (f *fakeInstallSource) rootDir() string { return f.dir }
func (f *fakeInstallSource) labels() sourceLabels {
	return sourceLabels{displayName: "fake", resourceNoun: "Fake source", promptVerb: "fake-install", failVerb: "fake-install", pastVerb: "Fake-installed", unitNoun: "skill(s)"}
}
func (f *fakeInstallSource) configSourceKey() string { return "fake" }
func (f *fakeInstallSource) confirmReplacementArgs() (string, bool, string) {
	return "fake", false, ""
}
func (f *fakeInstallSource) install(_, _, _ string) error                  { return nil }
func (f *fakeInstallSource) recordConfig(_ *config.Config, _, _, _ string) {}
func (f *fakeInstallSource) progressLine(name, subpath string) string {
	return "installing " + name + " " + subpath
}
func (f *fakeInstallSource) allowsSoleDiscoveredRename() bool { return f.allowRename }

func testCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "add"}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	return cmd
}

func TestResolveSkillsToInstallAllFlag(t *testing.T) {
	discovered := map[string]string{"one": "skills/one", "two": "skills/two"}
	src := &fakeInstallSource{dir: t.TempDir()}

	got, cancelled, err := resolveSkillsToInstall(testCmd(), discovered, src, true, "", nil, true)
	if err != nil || cancelled {
		t.Fatalf("resolveSkillsToInstall() = %v, %v, %v", got, cancelled, err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d skills; want 2 (%v)", len(got), got)
	}
}

func TestResolveSkillsToInstallPathMatchesDiscoveredSubpath(t *testing.T) {
	discovered := map[string]string{"one": "skills/one", "two": "skills/two"}
	src := &fakeInstallSource{dir: t.TempDir()}

	got, cancelled, err := resolveSkillsToInstall(testCmd(), discovered, src, false, "skills/two", nil, true)
	if err != nil || cancelled {
		t.Fatalf("resolveSkillsToInstall() = %v, %v, %v", got, cancelled, err)
	}
	if len(got) != 1 || got["two"] != "skills/two" {
		t.Fatalf("got %v; want only 'two'", got)
	}
}

func TestResolveSkillsToInstallPathFallsBackToSkillMD(t *testing.T) {
	root := t.TempDir()
	subDir := filepath.Join(root, "nested", "tool")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "SKILL.md"), []byte("# Tool"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &fakeInstallSource{dir: root}

	got, cancelled, err := resolveSkillsToInstall(testCmd(), map[string]string{}, src, false, "nested/tool", nil, true)
	if err != nil || cancelled {
		t.Fatalf("resolveSkillsToInstall() = %v, %v, %v", got, cancelled, err)
	}
	if len(got) != 1 || got["tool"] != "nested/tool" {
		t.Fatalf("got %v; want fallback skill 'tool'", got)
	}
}

func TestResolveSkillsToInstallPathWithoutSkillMDErrors(t *testing.T) {
	root := t.TempDir()
	src := &fakeInstallSource{dir: root}

	_, _, err := resolveSkillsToInstall(testCmd(), map[string]string{}, src, false, "missing/path", nil, true)
	if err == nil || !strings.Contains(err.Error(), "does not contain SKILL.md") {
		t.Fatalf("err = %v; want SKILL.md missing error", err)
	}
}

func TestResolveSkillsToInstallSkillFlagExactAndCaseInsensitiveMatch(t *testing.T) {
	discovered := map[string]string{"Api": "skills/api", "lint": "skills/lint"}
	src := &fakeInstallSource{dir: t.TempDir()}

	got, cancelled, err := resolveSkillsToInstall(testCmd(), discovered, src, false, "", []string{"lint", "api"}, true)
	if err != nil || cancelled {
		t.Fatalf("resolveSkillsToInstall() = %v, %v, %v", got, cancelled, err)
	}
	if len(got) != 2 || got["lint"] != "skills/lint" || got["Api"] != "skills/api" {
		t.Fatalf("got %v; want lint (exact) and Api (case-insensitive)", got)
	}
}

func TestResolveSkillsToInstallSkillFlagUnmatchedWarnsAndSkips(t *testing.T) {
	discovered := map[string]string{"lint": "skills/lint", "api": "skills/api"}
	src := &fakeInstallSource{dir: t.TempDir()}
	cmd := testCmd()

	got, cancelled, err := resolveSkillsToInstall(cmd, discovered, src, false, "", []string{"ghost"}, true)
	if err != nil || cancelled {
		t.Fatalf("resolveSkillsToInstall() = %v, %v, %v", got, cancelled, err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v; want no matches for an unknown skill", got)
	}
	out := cmd.OutOrStdout().(*bytes.Buffer).String()
	if !strings.Contains(out, "Skill 'ghost' not found") {
		t.Fatalf("output = %q; want a not-found warning", out)
	}
}

func TestResolveSkillsToInstallSingleSkillFlagRenamesSoleDiscoveredWhenAllowed(t *testing.T) {
	discovered := map[string]string{"original-name": "."}
	src := &fakeInstallSource{dir: t.TempDir(), allowRename: true}

	got, cancelled, err := resolveSkillsToInstall(testCmd(), discovered, src, false, "", []string{"renamed"}, true)
	if err != nil || cancelled {
		t.Fatalf("resolveSkillsToInstall() = %v, %v, %v", got, cancelled, err)
	}
	if len(got) != 1 || got["renamed"] != "." {
		t.Fatalf("got %v; want the sole discovered skill renamed to 'renamed'", got)
	}
}

// Remote installs never had the local-only sole-discovered-rename shortcut:
// a --skill name that doesn't match must fall through to exact/case-insensitive
// matching and simply not match, not silently rename the one discovered skill.
func TestResolveSkillsToInstallSingleSkillFlagDoesNotRenameWhenDisallowed(t *testing.T) {
	discovered := map[string]string{"original-name": "."}
	src := &fakeInstallSource{dir: t.TempDir(), allowRename: false}
	cmd := testCmd()

	got, cancelled, err := resolveSkillsToInstall(cmd, discovered, src, false, "", []string{"renamed"}, true)
	if err != nil || cancelled {
		t.Fatalf("resolveSkillsToInstall() = %v, %v, %v", got, cancelled, err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v; want no match, not a silent rename", got)
	}
	out := cmd.OutOrStdout().(*bytes.Buffer).String()
	if !strings.Contains(out, "Skill 'renamed' not found") {
		t.Fatalf("output = %q; want a not-found warning", out)
	}
}
