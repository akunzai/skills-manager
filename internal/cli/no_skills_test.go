package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/models"
	"github.com/akunzai/skills-manager/internal/wellknowntest"
)

// noSkillsSources is a Project Scope in a scratch directory with an empty
// local directory, a git Source holding no SKILL.md, and a well-known index
// listing nothing, each named as the state names it.
type noSkillsSources struct {
	scope              wellKnownScope
	local, localName   string
	origin             string
	index, indexSource string
}

func newNoSkillsSources(t *testing.T) noSkillsSources {
	t.Helper()
	s := noSkillsSources{scope: newWellKnownScope(t)}
	s.local = filepath.Join(s.scope.root, "empty")
	if err := os.MkdirAll(filepath.Join(s.local, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.localName = models.ToTildePath(s.local)
	s.origin = filepath.Join(s.scope.root, "origin")
	if err := os.MkdirAll(s.origin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.origin, "README.md"), []byte("# Nothing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitCLIGitRepo(t, s.origin)
	server := newWellKnownServer(t)
	s.indexSource = server.URL
	s.index = models.WellKnownIndexURL(server.URL)
	return s
}

func (s noSkillsSources) run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append(args, "-p", "--cache-dir", s.scope.cache))
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// An empty Source is a state, not a failure (ADR-0002): Add states it on
// stderr without an Error: prefix and exits 1, whatever kind the Source is.
func TestCLIAddReportsASourceThatOffersNoSkills(t *testing.T) {
	s := newNoSkillsSources(t)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"local", []string{s.local}, s.localName + " has no SKILL.md.\n"},
		{"git", []string{"owner/repo", "--url", s.origin}, "owner/repo has no SKILL.md.\n"},
		{"well-known", []string{s.indexSource}, s.index + " lists no Skills.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := s.run(t, append([]string{"add", "--yes"}, tc.args...)...)
			// A remote Source's fetch progress comes first.
			if ExitCode(err) != 1 || !strings.HasSuffix(stderr, tc.want) || strings.Contains(stderr, "Error:") {
				t.Fatalf("err = %v (exit %d), stderr %q; want exit 1 and %q", err, ExitCode(err), stderr, tc.want)
			}
			if strings.Contains(stdout, "Error:") || strings.Contains(stdout, tc.want) {
				t.Fatalf("stdout = %q; the state belongs on stderr only", stdout)
			}
		})
	}
}

// Discovery narrowed by --path names --path as the way out.
func TestCLIAddNamesPathWhenItNarrowedDiscovery(t *testing.T) {
	s := newNoSkillsSources(t)
	_, stderr, err := s.run(t, "add", "--yes", s.local, "--path", "docs")
	if want := s.localName + " has no SKILL.md.\nNext: try a path wider than docs, or none.\n"; ExitCode(err) != 1 || stderr != want {
		t.Fatalf("err = %v (exit %d), stderr %q; want %q", err, ExitCode(err), stderr, want)
	}
}

// --list keeps its result on stdout, with the same sentence.
func TestCLIAddListStatesASourceThatOffersNoSkills(t *testing.T) {
	s := newNoSkillsSources(t)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"local", []string{s.local}, s.localName + " has no SKILL.md."},
		{"git", []string{"owner/repo", "--url", s.origin}, "owner/repo has no SKILL.md."},
		{"well-known", []string{s.indexSource}, s.index + " lists no Skills."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, _, err := s.run(t, append([]string{"add", "--list"}, tc.args...)...)
			if ExitCode(err) != 1 || !strings.Contains(stdout, tc.want) {
				t.Fatalf("err = %v (exit %d), stdout %q; want exit 1 and %q", err, ExitCode(err), stdout, tc.want)
			}
			stdout, _, err = s.run(t, append([]string{"add", "--list", "--json"}, tc.args...)...)
			if ExitCode(err) != 1 || strings.TrimSpace(stdout) != "[]" {
				t.Fatalf("--json: err = %v (exit %d), stdout %q; want [] and exit 1", err, ExitCode(err), stdout)
			}
		})
	}
}

// The subpath may come from the Source string rather than --path; the way
// out names the subpath either way.
func TestCLIAddNamesASubpathFromTheSourceString(t *testing.T) {
	s := newNoSkillsSources(t)
	if err := os.MkdirAll(filepath.Join(s.origin, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.origin, "skills", "README.md"), []byte("# Nothing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cliRunGit(t, s.origin, "add", ".")
	cliRunGit(t, s.origin, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "skills")

	_, stderr, err := s.run(t, "add", "--yes", "owner/repo/skills", "--url", s.origin)
	want := "owner/repo has no SKILL.md.\nNext: try a path wider than skills, or none.\n"
	if ExitCode(err) != 1 || !strings.HasSuffix(stderr, want) || strings.Contains(stderr, "--path") {
		t.Fatalf("err = %v (exit %d), stderr %q; want %q", err, ExitCode(err), stderr, want)
	}
}

// use states an empty Source with the same sentence as add, for each kind.
func TestCLIUseStatesASourceThatOffersNoSkills(t *testing.T) {
	s := newNoSkillsSources(t)
	stdout, stderr, err := s.run(t, "use", "owner/repo", "--url", s.origin)
	if want := "owner/repo has no SKILL.md.\n"; ExitCode(err) != 1 || stdout != "" || stderr != want {
		t.Fatalf("git: err = %v (exit %d), stdout %q, stderr %q; want %q", err, ExitCode(err), stdout, stderr, want)
	}

	// An index whose every entry is skipped lists none.
	odd := wellknowntest.SkillMD("odd", "Odd")
	odd.Type = "bundle"
	server := newWellKnownServer(t, odd)
	stdout, stderr, err = s.run(t, "use", server.URL)
	want := models.WellKnownIndexURL(server.URL) + " lists no Skills.\n"
	if ExitCode(err) != 1 || stdout != "" || !strings.HasSuffix(stderr, want) || !strings.Contains(stderr, `unknown type "bundle"`) {
		t.Fatalf("well-known: err = %v (exit %d), stdout %q, stderr %q; want the skip warning, then %q", err, ExitCode(err), stdout, stderr, want)
	}
}
