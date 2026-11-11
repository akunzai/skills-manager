package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
)

// catScope is a Project Scope whose Config declares a remote Skill on disk
// ("remote"), one that is not ("absent"), a symlink Skill ("linked"), and
// whose skills directory also holds an Untracked Skill ("loose") and an
// Untracked directory with no SKILL.md ("empty").
type catScope struct {
	configFile, skillsDir, cacheDir string
}

func newCatScope(t *testing.T) catScope {
	t.Helper()
	isolateHome(t)
	project := t.TempDir()
	t.Chdir(project)
	s := catScope{
		configFile: filepath.Join(project, ".agents", "skills.json"),
		skillsDir:  filepath.Join(project, ".agents", "skills"),
		cacheDir:   filepath.Join(project, "cache"),
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(s.skillsDir, "remote", "SKILL.md"), "---\nname: remote\n---\n\n# Remote\n")
	write(filepath.Join(s.skillsDir, "remote", "helper.sh"), "echo hi\n")
	write(filepath.Join(s.skillsDir, "loose", "SKILL.md"), "# Loose\n")
	if err := os.MkdirAll(filepath.Join(s.skillsDir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	linkSource := filepath.Join(project, "src", "linked")
	write(filepath.Join(linkSource, "SKILL.md"), "# Linked\n")
	if err := os.Symlink(linkSource, filepath.Join(s.skillsDir, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "remote", "remote", "github", "")
	config.AddRemoteSkillEntry(cfg, "owner/repo", "absent", "absent", "github", "")
	config.AddLocalSymlinkEntry(cfg, "linked", linkSource, "")
	if err := config.SaveConfig(cfg, s.configFile); err != nil {
		t.Fatal(err)
	}
	return s
}

func (s catScope) run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append(append([]string{"cat"}, args...), "-p", "--config", s.configFile, "--skills-dir", s.skillsDir, "--cache-dir", s.cacheDir))
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// cat prints exactly the SKILL.md an Agent reads, whatever kind the Skill is.
func TestCLICatPrintsTheScopeCopyByteForByte(t *testing.T) {
	s := newCatScope(t)
	for _, name := range []string{"remote", "linked", "loose"} {
		want, err := os.ReadFile(filepath.Join(s.skillsDir, name, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		stdout, stderr, err := s.run(t, name)
		if err != nil {
			t.Fatalf("cat %s: %v\n%s", name, err, stderr)
		}
		if stdout != string(want) {
			t.Errorf("cat %s stdout = %q; want the file's bytes %q", name, stdout, want)
		}
		if stderr != "" {
			t.Errorf("cat %s wrote to stderr: %q", name, stderr)
		}
	}
}

// A Skill cat cannot read is a state with a next action (exit 1), and
// stdout stays empty so a pipe never receives a partial or wrong Skill.
func TestCLICatReportsAnUnreadableSkillWithItsNextCommand(t *testing.T) {
	s := newCatScope(t)
	for _, tt := range []struct{ name, next string }{
		{"absent", "Next: run 'skills sync -p"},
		{"empty", "Next: run 'skills doctor -p"},
	} {
		stdout, stderr, err := s.run(t, tt.name)
		if err == nil || ExitCode(err) != 1 {
			t.Fatalf("cat %s = %v; want exit 1", tt.name, err)
		}
		if stdout != "" {
			t.Errorf("cat %s stdout = %q; want nothing", tt.name, stdout)
		}
		if !strings.Contains(stderr, tt.name) || !strings.Contains(stderr, tt.next) {
			t.Errorf("cat %s stderr = %q; want the Skill named and %q", tt.name, stderr, tt.next)
		}
	}
}

func TestCLICatRefusesAnUnknownSkillAndWrongArgs(t *testing.T) {
	s := newCatScope(t)
	stdout, _, err := s.run(t, "nope")
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "no Skill nope in this Scope") {
		t.Fatalf("cat nope = %v; want exit 2 naming the Skill", err)
	}
	if stdout != "" {
		t.Errorf("cat nope stdout = %q; want nothing", stdout)
	}
	// Names match as ls shows them, case-sensitively.
	if _, _, err := s.run(t, "Remote"); err == nil || ExitCode(err) != 2 {
		t.Fatalf("cat Remote = %v; want exit 2", err)
	}
	for _, args := range [][]string{{}, {"remote", "loose"}} {
		if _, _, err := s.run(t, args...); err == nil || ExitCode(err) != 2 {
			t.Fatalf("cat %v = %v; want a usage error, exit 2", args, err)
		}
	}
}
