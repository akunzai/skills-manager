package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/signing"
)

// useFixture is an isolated home with a Cache directory, and the Config and
// skills directory use must never create.
type useFixture struct {
	home, configFile, skillsDir, cacheDir string
}

func newUseFixture(t *testing.T) useFixture {
	t.Helper()
	home := isolateHome(t)
	return useFixture{
		home:       home,
		configFile: filepath.Join(home, ".agents", "skills.json"),
		skillsDir:  filepath.Join(home, ".agents", "skills"),
		cacheDir:   filepath.Join(home, ".agents", "cache"),
	}
}

// origin is a git repository holding a Skill directory with body as its
// SKILL.md for each of skills, plus any extra files (path to content).
func (f useFixture) origin(t *testing.T, skills map[string]string, extra map[string]string) string {
	t.Helper()
	repo := filepath.Join(f.home, "origin")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range skills {
		write(filepath.Join(repo, name, "SKILL.md"), body)
	}
	for path, body := range extra {
		write(filepath.Join(repo, filepath.FromSlash(path)), body)
	}
	commitCLIGitRepo(t, repo)
	return repo
}

// stubTrustRoot makes fetching the Sigstore trust root return data, or err.
func stubTrustRoot(t *testing.T, data []byte, err error) {
	t.Helper()
	previous := engine.FetchTrustedRoot
	engine.FetchTrustedRoot = func(string) ([]byte, error) { return data, err }
	t.Cleanup(func() { engine.FetchTrustedRoot = previous })
}

func (f useFixture) run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append(append([]string{"use"}, args...), "--config", f.configFile, "--skills-dir", f.skillsDir, "--cache-dir", f.cacheDir))
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// use prints exactly the SKILL.md and declares nothing: no Config, no Scope
// skills directory.
func TestCLIUsePrintsALoneSkillByteForByte(t *testing.T) {
	f := newUseFixture(t)
	body := "---\nname: sample\ndescription: A sample\n---\n\n# Sample\n\nno trailing newline"
	origin := f.origin(t, map[string]string{"sample": body}, nil)

	stdout, stderr, err := f.run(t, "owner/repo", "--url", origin)
	if err != nil {
		t.Fatalf("use: %v\n%s", err, stderr)
	}
	if stdout != body {
		t.Fatalf("stdout = %q, want the SKILL.md bytes %q", stdout, body)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q; an unsigned Skill prints nothing else", stderr)
	}
	for _, path := range []string{f.configFile, f.skillsDir} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("%s exists (err %v); use must declare nothing", path, statErr)
		}
	}
}

// A Source with several Skills needs -s, and only that Skill is printed.
func TestCLIUseSelectsOneOfSeveralSkills(t *testing.T) {
	f := newUseFixture(t)
	origin := f.origin(t, map[string]string{"alpha": "# Alpha\n", "beta": "# Beta\n"}, nil)

	stdout, stderr, err := f.run(t, "owner/repo", "--url", origin)
	if ExitCode(err) != 2 || stdout != "" || !strings.Contains(err.Error()+stderr, "skills add owner/repo --url "+origin+" --list") {
		t.Fatalf("without -s: err = %v (exit %d), stdout %q, stderr %q; want exit 2 naming add --list", err, ExitCode(err), stdout, stderr)
	}

	stdout, stderr, err = f.run(t, "owner/repo", "--url", origin, "-s", "beta")
	if err != nil || stdout != "# Beta\n" {
		t.Fatalf("-s beta: err = %v, stdout %q, stderr %q; want only beta", err, stdout, stderr)
	}

	stdout, _, err = f.run(t, "owner/repo", "--url", origin, "-s", "gamma")
	if ExitCode(err) != 2 || stdout != "" || !strings.Contains(err.Error(), "gamma") {
		t.Fatalf("-s gamma: err = %v (exit %d), stdout %q; want exit 2 naming gamma", err, ExitCode(err), stdout)
	}
}

func TestCLIUseRefusesASkillWhoseSignatureDoesNotVerify(t *testing.T) {
	f := newUseFixture(t)
	origin := f.origin(t, map[string]string{"sample": "# Sample\n"}, map[string]string{"sample/" + signing.FileName: "{}"})
	stubTrustRoot(t, []byte(`{"mediaType":"application/vnd.dev.sigstore.trustedroot+json;version=0.1"}`), nil)

	stdout, stderr, err := f.run(t, "owner/repo", "--url", origin)
	if ExitCode(err) != 1 || stdout != "" || !strings.Contains(stderr, "sample from owner/repo is not printed: its signature does not verify") || strings.Contains(stderr, "Error:") {
		t.Fatalf("err = %v (exit %d), stdout %q, stderr %q; want exit 1 stating the signature, no Error:", err, ExitCode(err), stdout, stderr)
	}
}

// A trust root that cannot be fetched is a failure to read the Skill, not
// its state (ADR-0002).
func TestCLIUseFailsWhenTheTrustRootCannotBeFetched(t *testing.T) {
	f := newUseFixture(t)
	origin := f.origin(t, map[string]string{"sample": "# Sample\n"}, map[string]string{"sample/" + signing.FileName: "{}"})
	stubTrustRoot(t, nil, errors.New("tuf.sigstore.dev unreachable"))

	stdout, _, err := f.run(t, "owner/repo", "--url", origin)
	if ExitCode(err) != 2 || stdout != "" || !strings.Contains(err.Error(), "trust root") {
		t.Fatalf("err = %v (exit %d), stdout %q; want exit 2 naming the trust root", err, ExitCode(err), stdout)
	}
}

func TestCLIUseFailsOnASourceItCannotFetch(t *testing.T) {
	f := newUseFixture(t)

	stdout, _, err := f.run(t, "owner/repo", "--url", filepath.Join(f.home, "does-not-exist"))
	if ExitCode(err) != 2 || stdout != "" || !strings.Contains(err.Error(), "owner/repo") {
		t.Fatalf("err = %v (exit %d), stdout %q; want exit 2 naming the Source", err, ExitCode(err), stdout)
	}
}

func TestCLIUseReportsASourceWithNoSkills(t *testing.T) {
	f := newUseFixture(t)
	origin := f.origin(t, nil, map[string]string{"README.md": "# Nothing\n"})

	stdout, stderr, err := f.run(t, "owner/repo", "--url", origin)
	if ExitCode(err) != 1 || stdout != "" || stderr != "owner/repo has no SKILL.md.\n" {
		t.Fatalf("err = %v (exit %d), stdout %q, stderr %q; want exit 1 stating the missing SKILL.md", err, ExitCode(err), stdout, stderr)
	}
}

func TestCLIUseUsageErrors(t *testing.T) {
	f := newUseFixture(t)
	// Cobra prints usage on stdout for these, as for every command.
	for _, args := range [][]string{
		{},
		{"owner/repo", "other/repo"},
		{"owner/repo", "--symlink", "./local"},
		{"owner/repo", "--command", "echo"},
	} {
		if _, _, err := f.run(t, args...); ExitCode(err) != 2 {
			t.Errorf("use %v: err = %v (exit %d); want exit 2", args, err, ExitCode(err))
		}
	}
	stdout, _, err := f.run(t, "./local")
	if ExitCode(err) != 2 || stdout != "" || !strings.Contains(err.Error(), "skills cat") {
		t.Fatalf("use ./local: err = %v (exit %d), stdout %q; want exit 2 pointing at cat", err, ExitCode(err), stdout)
	}
}

// A keyless signature verifies end to end, and its Signer is named on stderr
// without being recorded.
func TestCLIUseVerifiesAKeylessSkillAndNamesItsSigner(t *testing.T) {
	f := newUseFixture(t)
	body, err := os.ReadFile(filepath.Join(miseFixture, "mise", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := os.ReadFile(filepath.Join(miseFixture, "mise", signing.FileName))
	if err != nil {
		t.Fatal(err)
	}
	origin := f.origin(t, map[string]string{"mise": string(body)}, map[string]string{"mise/" + signing.FileName: string(sig)})
	fetches := offlineTrustRoot(t)

	stdout, stderr, err := f.run(t, "owner/repo", "--url", origin)
	if err != nil {
		t.Fatalf("use: %v\n%s", err, stderr)
	}
	if stdout != string(body) || *fetches != 1 {
		t.Fatalf("stdout = %q, fetches %d; want the signed SKILL.md after one trust root fetch", stdout, *fetches)
	}
	if want := "Signed by " + miseSigner; strings.Count(stderr, "\n") != 1 || !strings.Contains(stderr, want) || !strings.Contains(stderr, "not recorded") {
		t.Fatalf("stderr = %q; want one line naming %q", stderr, want)
	}
	if _, statErr := os.Stat(f.configFile); !os.IsNotExist(statErr) {
		t.Fatalf("Config exists (err %v); the Signer must not be recorded", statErr)
	}
}

// A Source some Scope declares, on another branch even, is read like any
// other, and that Scope is left exactly as it was.
func TestCLIUseLeavesADeclaringScopeUntouched(t *testing.T) {
	f := newUseFixture(t)
	origin := f.origin(t, map[string]string{"sample": "# Sample\n"}, nil)
	cfg := config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "github", origin)
	repo := cfg.Remote["owner/repo"]
	repo.Branch = "elsewhere"
	cfg.Remote["owner/repo"] = repo
	if err := config.SaveConfig(cfg, f.configFile); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.skillsDir, "sample"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.skillsDir, "sample", "SKILL.md"), []byte("# Local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheRel, err := filepath.Rel(f.home, f.cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, f.home, cacheRel, "origin")

	stdout, stderr, err := f.run(t, "owner/repo", "--url", origin)
	if err != nil || stdout != "# Sample\n" {
		t.Fatalf("use: err = %v, stdout %q, stderr %q; want the Source's SKILL.md", err, stdout, stderr)
	}
	if after := snapshotTree(t, f.home, cacheRel, "origin"); !reflect.DeepEqual(before, after) {
		t.Fatalf("use changed the Scope\nbefore = %#v\nafter  = %#v", before, after)
	}
}
