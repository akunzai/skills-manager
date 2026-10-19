package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/wellknowntest"
)

// newWellKnownServer publishes entries over TLS and points the engine's
// well-known client at it for the rest of the test.
func newWellKnownServer(t *testing.T, entries ...wellknowntest.Entry) *wellknowntest.Server {
	t.Helper()
	s := wellknowntest.NewServer(t, entries...)
	previous := engine.WellKnownHTTPClient
	engine.WellKnownHTTPClient = s.Client()
	t.Cleanup(func() { engine.WellKnownHTTPClient = previous })
	return s
}

// wellKnownScope is a Project Scope in a scratch directory, with its own
// Cache directory: Add and Sync never reach the developer's home.
type wellKnownScope struct {
	root, config, skills, cache string
}

func newWellKnownScope(t *testing.T) wellKnownScope {
	t.Helper()
	home := isolateHome(t)
	root := filepath.Join(home, "project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return wellKnownScope{
		root:   root,
		config: filepath.Join(root, ".agents", "skills.json"),
		skills: filepath.Join(root, ".agents", "skills"),
		cache:  filepath.Join(home, "cache"),
	}
}

func (s wellKnownScope) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return runCLI(t, append(args, "-p", "--cache-dir", s.cache)...)
}

// stdout runs the command and returns only its standard output, as a
// machine reading --json would.
func (s wellKnownScope) stdout(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append(args, "-p", "--cache-dir", s.cache))
	err := cmd.Execute()
	if err != nil {
		return stdout.String() + stderr.String(), err
	}
	return stdout.String(), nil
}

func TestCLIAddListShowsTheSkillsAWellKnownIndexOffers(t *testing.T) {
	server := newWellKnownServer(t,
		wellknowntest.SkillMD("recheck-lint", "Lint Markdown"),
		wellknowntest.TarGz(t, "agents-sdk", "Build agents", wellknowntest.File{Name: "SKILL.md", Body: wellknowntest.SkillDoc("agents-sdk")}),
	)
	scope := newWellKnownScope(t)

	out, err := scope.run(t, "add", server.URL, "--list")
	if err != nil {
		t.Fatalf("add --list: %v\n%s", err, out)
	}
	for _, want := range []string{"agents-sdk", "Build agents", "recheck-lint", "Lint Markdown", "2 skills found."} {
		if !strings.Contains(out, want) {
			t.Errorf("add --list output lacks %q:\n%s", want, out)
		}
	}

	out, err = scope.stdout(t, "add", server.URL+"/.well-known/agent-skills/index.json", "--list", "--json")
	if err != nil {
		t.Fatalf("add --list --json: %v\n%s", err, out)
	}
	var rows []addListRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("parse --list --json: %v\n%s", err, out)
	}
	want := []addListRow{{Name: "agents-sdk", Path: "agents-sdk", Description: "Build agents"}, {Name: "recheck-lint", Path: "recheck-lint", Description: "Lint Markdown"}}
	if len(rows) != len(want) || rows[0] != want[0] || rows[1] != want[1] {
		t.Fatalf("rows = %+v, want %+v", rows, want)
	}
	if _, err := os.Stat(scope.config); !os.IsNotExist(err) {
		t.Fatalf("add --list wrote Config: %v", err)
	}
}

func TestCLIAddDeclaresAndMaterializesWellKnownSkillsThatSyncKeepsOffline(t *testing.T) {
	server := newWellKnownServer(t,
		wellknowntest.SkillMD("recheck-lint", "Lint Markdown"),
		wellknowntest.TarGz(t, "agents-sdk", "Build agents",
			wellknowntest.File{Name: "./"},
			wellknowntest.File{Name: "./SKILL.md", Body: wellknowntest.SkillDoc("agents-sdk")},
			wellknowntest.File{Name: "./references/api.md", Body: "# API\n"},
			wellknowntest.File{Name: "./scripts/run.sh", Body: "#!/bin/sh\n", Mode: 0o755},
		),
		wellknowntest.Zip(t, "zipped", "Zipped skill", wellknowntest.File{Name: "SKILL.md", Body: wellknowntest.SkillDoc("zipped")}, wellknowntest.File{Name: "notes/a.md", Body: "a\n"}),
	)
	scope := newWellKnownScope(t)

	out, err := scope.run(t, "add", server.URL, "--all", "-y")
	if err != nil {
		t.Fatalf("add --all: %v\n%s", err, out)
	}
	for path, want := range map[string]string{
		"agents-sdk/SKILL.md":          wellknowntest.SkillDoc("agents-sdk"),
		"agents-sdk/references/api.md": "# API\n",
		"recheck-lint/SKILL.md":        string(wellknowntest.SkillMD("recheck-lint", "Lint Markdown").Body),
		"zipped/notes/a.md":            "a\n",
	} {
		got, err := os.ReadFile(filepath.Join(scope.skills, filepath.FromSlash(path)))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", path, got, err, want)
		}
	}

	data, err := os.ReadFile(scope.config)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Remote map[string]map[string]any `json:"remote"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	key := strings.TrimPrefix(server.URL, "https://")
	repo := cfg.Remote[key]
	if repo["type"] != "well-known" || repo["url"] != server.URL || repo["branch"] != nil {
		t.Fatalf("remote[%s] = %v; want type well-known, url %s, no branch\n%s", key, repo, server.URL, data)
	}
	skills, _ := repo["skills"].(map[string]any)
	if len(skills) != 3 || skills["agents-sdk"] != "agents-sdk" || skills["recheck-lint"] != "recheck-lint" {
		t.Fatalf("skills = %v", skills)
	}

	baselines := engine.OpenBaselines(scope.skills)
	applied, ok := baselines.Applied("agents-sdk")
	sum := sha256.Sum256(server.Entries[1].Body)
	if !ok || applied.CacheIdentity != server.URL || applied.AppliedCommit != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("Baseline = %+v, %v; want CacheIdentity %s and the entry digest", applied, ok, server.URL)
	}

	// Sync reads the Cache only: with the publisher gone, a removed Scope
	// copy is restored, and a local edit is protected as Drift.
	server.Close()
	if err := os.RemoveAll(filepath.Join(scope.skills, "recheck-lint")); err != nil {
		t.Fatal(err)
	}
	out, err = scope.run(t, "sync")
	if err != nil {
		t.Fatalf("offline sync: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(scope.skills, "recheck-lint", "SKILL.md")); err != nil {
		t.Fatalf("sync did not restore recheck-lint: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(scope.skills, "agents-sdk", "SKILL.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = scope.run(t, "sync")
	if ExitCode(err) != 1 || !strings.Contains(out, "agents-sdk") {
		t.Fatalf("sync over Drift = %v (exit %d); want exit 1 naming agents-sdk\n%s", err, ExitCode(err), out)
	}
	if got, _ := os.ReadFile(filepath.Join(scope.skills, "agents-sdk", "SKILL.md")); string(got) != "edited\n" {
		t.Fatalf("sync overwrote the Drifted copy: %q", got)
	}
}

// Which artifacts are refused is the engine's (wellknown_cache_test.go);
// this pins how Add reports one: exit 1, with nothing declared or
// Materialized.
func TestCLIAddRefusesAWellKnownArtifactItCannotVerify(t *testing.T) {
	entry := wellknowntest.SkillMD("bad", "Bad")
	entry.Digest = "sha256:" + strings.Repeat("0", 64)
	server := newWellKnownServer(t, entry)
	scope := newWellKnownScope(t)

	out, err := scope.run(t, "add", server.URL, "--skill", "bad", "-y")
	if ExitCode(err) != 1 || !strings.Contains(out, "Error:") || !strings.Contains(out, "does not match its digest") {
		t.Fatalf("add = %v (exit %d); want exit 1 saying the digest does not match\n%s", err, ExitCode(err), out)
	}
	if _, err := os.Stat(filepath.Join(scope.skills, "bad")); !os.IsNotExist(err) {
		t.Fatalf("a refused Skill was Materialized: %v", err)
	}
	if _, err := os.Stat(scope.config); !os.IsNotExist(err) {
		t.Fatalf("a refused Skill was declared: %v", err)
	}
}

// An archive's format comes from its Content-Type, and from its extension
// only when the type is absent or generic; a redirect to https:// is
// followed.
func TestCLIAddReadsAWellKnownArchiveByContentTypeThenExtension(t *testing.T) {
	zipped := wellknowntest.Zip(t, "zipped", "Zipped", wellknowntest.File{Name: "SKILL.md", Body: wellknowntest.SkillDoc("zipped")})
	zipped.URL = "/.well-known/agent-skills/zipped.tar.gz"
	generic := wellknowntest.TarGz(t, "generic", "Generic", wellknowntest.File{Name: "SKILL.md", Body: wellknowntest.SkillDoc("generic")})
	generic.ContentType = "application/octet-stream"
	moved := wellknowntest.SkillMD("moved", "Moved")
	moved.URL = "/redirect-https"
	server := newWellKnownServer(t, zipped, generic, moved)
	server.Extra["/redirect-https"] = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/.well-known/agent-skills/moved/SKILL.md", http.StatusFound)
	}
	server.Extra["/.well-known/agent-skills/moved/SKILL.md"] = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(moved.Body)
	}
	scope := newWellKnownScope(t)

	out, err := scope.run(t, "add", server.URL, "--all", "--agent", "claude", "-y")
	if err != nil {
		t.Fatalf("add --all --agent claude: %v\n%s", err, out)
	}
	for _, name := range []string{"zipped", "generic", "moved"} {
		if _, err := os.Stat(filepath.Join(scope.skills, name, "SKILL.md")); err != nil {
			t.Errorf("%s not Materialized: %v\n%s", name, err, out)
		}
		link := filepath.Join(scope.root, ".claude", "skills", name)
		if _, err := os.Stat(link); err != nil {
			t.Errorf("%s not available to Claude Code: %v", name, err)
		}
	}
}

// Which well-known Sources Add refuses is the engine's (ClassifyAddKind's
// tests); this pins how Add reports one, before the Scope is asked for, and
// what it suggests for an index it cannot read.
func TestCLIAddRefusesWellKnownSourcesItDoesNotRead(t *testing.T) {
	server := newWellKnownServer(t, wellknowntest.SkillMD("one", "One"))
	scope := newWellKnownScope(t)

	out, err := scope.run(t, "add", server.URL, "--branch", "main", "-y")
	if ExitCode(err) != 1 || !strings.Contains(out, "Error: --branch does not apply") {
		t.Fatalf("add --branch = %v (exit %d); want exit 1 saying --branch does not apply\n%s", err, ExitCode(err), out)
	}
	if got := server.Requested("/"); len(got) != 0 {
		t.Fatalf("a refused Source was requested: %v", got)
	}

	server.Schema = ""
	out, err = scope.run(t, "add", server.URL, "--list")
	if ExitCode(err) != 1 || !strings.Contains(out, "v0.1.0") || !strings.Contains(out, "Next:") || !strings.Contains(out, "git repository") {
		t.Fatalf("v0.1.0 index: add = %v (exit %d); want exit 1 naming the git Source\n%s", err, ExitCode(err), out)
	}
}

func TestCLIAddSkipsWellKnownEntriesOfAnUnknownType(t *testing.T) {
	odd := wellknowntest.SkillMD("odd", "Odd")
	odd.Type = "bundle"
	server := newWellKnownServer(t, wellknowntest.SkillMD("one", "One"), odd)
	scope := newWellKnownScope(t)

	out, err := scope.run(t, "add", server.URL, "--list")
	if err != nil {
		t.Fatalf("add --list: %v\n%s", err, out)
	}
	if !strings.Contains(out, `unknown type "bundle"`) || strings.Contains(out, "odd (odd)") || !strings.Contains(out, "1 skill found.") {
		t.Fatalf("want odd skipped with a warning and one listed:\n%s", out)
	}
}

func TestCLIOutdatedAndUpdateFollowAWellKnownSkillsDigest(t *testing.T) {
	server := newWellKnownServer(t, wellknowntest.SkillMD("one", "One"), wellknowntest.SkillMD("other", "Other"))
	scope := newWellKnownScope(t)
	if out, err := scope.run(t, "add", server.URL, "--skill", "one", "-y"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	server.Requested("")

	// An unchanged index is revalidated, whatever its max-age says, and
	// answered 304.
	out, err := scope.run(t, "outdated")
	if err != nil {
		t.Fatalf("outdated on an unchanged index = %v (exit %d)\n%s", err, ExitCode(err), out)
	}
	if got := server.Requested("/"); len(got) != 1 || got[0] != "GET /.well-known/agent-skills/index.json 304" {
		t.Fatalf("outdated requests = %v; want one conditional index request answered 304", got)
	}

	// A new digest is outdated, and update brings it in.
	server.SetEntries(wellknowntest.SkillMDBody("one", "One", "# one, revised\n"), wellknowntest.SkillMD("other", "Other, revised"))
	out, err = scope.run(t, "outdated")
	if ExitCode(err) != 1 || !strings.Contains(out, "one") || !strings.Contains(out, "changed upstream") {
		t.Fatalf("outdated after a new digest = %v (exit %d); want exit 1 naming one\n%s", err, ExitCode(err), out)
	}
	out, err = scope.run(t, "update")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if got, _ := os.ReadFile(filepath.Join(scope.skills, "one", "SKILL.md")); string(got) != "# one, revised\n" {
		t.Fatalf("one after update = %q\n%s", got, out)
	}
	for _, r := range server.Requested("/.well-known/agent-skills/other") {
		t.Errorf("update downloaded an undeclared Skill: %s", r)
	}
	applied, _ := engine.OpenBaselines(scope.skills).Applied("one")
	if applied.AppliedCommit != server.Entries[0].Digest {
		t.Fatalf("Baseline commit = %s; want %s", applied.AppliedCommit, server.Entries[0].Digest)
	}
	if out, err := scope.run(t, "outdated"); err != nil {
		t.Fatalf("outdated after update = %v (exit %d)\n%s", err, ExitCode(err), out)
	}
}

func TestCLIUpdateProtectsADriftedWellKnownSkillAndDiffShowsTheChange(t *testing.T) {
	server := newWellKnownServer(t, wellknowntest.SkillMDBody("one", "One", "line one\n"))
	scope := newWellKnownScope(t)
	if out, err := scope.run(t, "add", server.URL, "--all", "-y"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	server.SetEntries(wellknowntest.SkillMDBody("one", "One", "line one\nline two\n"))

	out, err := scope.run(t, "diff", "one", "--fetch")
	if ExitCode(err) != 1 || !strings.Contains(out, "+line two") {
		t.Fatalf("diff --fetch = %v (exit %d); want exit 1 showing +line two\n%s", err, ExitCode(err), out)
	}

	scopeCopy := filepath.Join(scope.skills, "one", "SKILL.md")
	if err := os.WriteFile(scopeCopy, []byte("my edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = scope.run(t, "update")
	if ExitCode(err) != 1 {
		t.Fatalf("update over Drift = %v (exit %d); want exit 1\n%s", err, ExitCode(err), out)
	}
	if got, _ := os.ReadFile(scopeCopy); string(got) != "my edit\n" {
		t.Fatalf("update overwrote the Drifted copy: %q", got)
	}
	// As for a git Source, Update's --force refetches but keeps a local
	// edit; only Sync's --force overwrites it.
	out, err = scope.run(t, "update", "--force")
	if ExitCode(err) != 1 {
		t.Fatalf("update --force over Drift = %v (exit %d); want exit 1\n%s", err, ExitCode(err), out)
	}
	if got, _ := os.ReadFile(scopeCopy); string(got) != "my edit\n" {
		t.Fatalf("update --force overwrote the Drifted copy: %q", got)
	}
	out, err = scope.run(t, "sync", "--force")
	if err != nil {
		t.Fatalf("sync --force: %v\n%s", err, out)
	}
	if got, _ := os.ReadFile(scopeCopy); string(got) != "line one\nline two\n" {
		t.Fatalf("one after sync --force = %q", got)
	}
}

func TestCLIUpdateFollowsAWellKnownRenameOrReportsTheRemoval(t *testing.T) {
	server := newWellKnownServer(t, wellknowntest.SkillMD("old", "Old"), wellknowntest.SkillMD("gone", "Gone"), wellknowntest.SkillMD("bystander", "Bystander"))
	scope := newWellKnownScope(t)
	if out, err := scope.run(t, "add", server.URL, "--skill", "old", "--skill", "gone", "-y"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	successor := wellknowntest.SkillMDBody("new", "New", "---\nname: new\ndescription: New\nmetadata:\n  replaces: old\n---\n# new\n")
	server.SetEntries(successor, wellknowntest.SkillMD("bystander", "Bystander"))

	out, err := scope.run(t, "update")
	if ExitCode(err) != 1 || !strings.Contains(out, "removed_upstream") || !strings.Contains(out, "skills rm -p --cache-dir ~/cache gone") {
		t.Fatalf("update = %v (exit %d); want exit 1 naming 'skills rm gone'\n%s", err, ExitCode(err), out)
	}
	if _, err := os.Stat(filepath.Join(scope.skills, "new", "SKILL.md")); err != nil {
		t.Fatalf("the successor of old was not Materialized: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(scope.skills, "old")); !os.IsNotExist(err) {
		t.Fatalf("old was not Retired: %v", err)
	}
	data, _ := os.ReadFile(scope.config)
	if !strings.Contains(string(data), `"new": "new"`) || strings.Contains(string(data), `"old"`) {
		t.Fatalf("Config does not follow the rename:\n%s", data)
	}

	// Once nothing declared is missing, no other entry is downloaded.
	if out, err := scope.run(t, "rm", "gone"); err != nil {
		t.Fatalf("rm gone: %v\n%s", err, out)
	}
	server.SetEntries(successor, wellknowntest.SkillMDBody("bystander", "Bystander", "# changed\n"))
	server.Requested("")
	if out, err := scope.run(t, "update"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	for _, r := range server.Requested("/.well-known/agent-skills/bystander") {
		t.Errorf("update downloaded an undeclared Skill with nothing to rename: %s", r)
	}
}
