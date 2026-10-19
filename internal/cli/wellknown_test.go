package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/akunzai/skills-manager/internal/engine"
)

// wellKnownEntry is one artifact a wellKnownServer serves, and the index
// entry naming it.
type wellKnownEntry struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Digest      string `json:"digest"`

	body        []byte
	contentType string
}

// wellKnownServer is a publisher of an Agent Skills Discovery index over
// TLS. Its index and artifacts may change between requests.
type wellKnownServer struct {
	*httptest.Server
	mu      sync.Mutex
	schema  string
	entries []wellKnownEntry
	// extra serves additional paths, such as a redirect.
	extra map[string]http.HandlerFunc
	// requests logs each request as "<method> <path> <status>".
	requests []string
}

// requested is the logged requests whose path has prefix, then clears the log.
func (s *wellKnownServer) requested(prefix string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var matched []string
	for _, r := range s.requests {
		if strings.HasPrefix(strings.Fields(r)[1], prefix) {
			matched = append(matched, r)
		}
	}
	s.requests = nil
	return matched
}

const wellKnownSchema = "https://schemas.agentskills.io/discovery/0.2.0/schema.json"

func newWellKnownServer(t *testing.T, entries ...wellKnownEntry) *wellKnownServer {
	t.Helper()
	s := &wellKnownServer{schema: wellKnownSchema, extra: map[string]http.HandlerFunc{}}
	s.setEntries(entries...)
	s.Server = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	previous := engine.WellKnownHTTPClient
	engine.WellKnownHTTPClient = s.Client()
	t.Cleanup(func() { engine.WellKnownHTTPClient = previous })
	return s
}

// setEntries replaces what the server publishes. An entry with no Digest
// gets the digest of its body; one with no URL is served at the conventional
// path.
func (s *wellKnownServer) setEntries(entries ...wellKnownEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range entries {
		if entries[i].Digest == "" {
			sum := sha256.Sum256(entries[i].body)
			entries[i].Digest = "sha256:" + hex.EncodeToString(sum[:])
		}
		if entries[i].URL == "" {
			entries[i].URL = "/.well-known/agent-skills/" + entries[i].Name + artifactExtension(entries[i])
		}
	}
	s.entries = entries
}

func artifactExtension(entry wellKnownEntry) string {
	switch {
	case entry.Type == "skill-md":
		return "/SKILL.md"
	case entry.contentType == "application/zip":
		return ".zip"
	default:
		return ".tar.gz"
	}
}

func (s *wellKnownServer) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if handler, ok := s.extra[r.URL.Path]; ok {
		handler(w, r)
		return
	}
	if r.URL.Path == "/.well-known/agent-skills/index.json" {
		index := map[string]any{"skills": s.entries}
		if s.schema != "" {
			index["$schema"] = s.schema
		}
		data, _ := json.Marshal(index)
		sum := sha256.Sum256(data)
		etag := `"` + hex.EncodeToString(sum[:8]) + `"`
		// A max-age the client must not take as leave to skip asking.
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			s.requests = append(s.requests, r.Method+" "+r.URL.Path+" 304")
			w.WriteHeader(http.StatusNotModified)
			return
		}
		s.requests = append(s.requests, r.Method+" "+r.URL.Path+" 200")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
		return
	}
	s.requests = append(s.requests, r.Method+" "+r.URL.Path)
	for _, entry := range s.entries {
		if entry.URL == r.URL.Path {
			if entry.contentType != "" {
				w.Header().Set("Content-Type", entry.contentType)
			}
			_, _ = w.Write(entry.body)
			return
		}
	}
	http.NotFound(w, r)
}

func skillMDEntry(name, description string) wellKnownEntry {
	return skillMDBody(name, description, "---\nname: "+name+"\ndescription: "+description+"\n---\n# "+name+"\n")
}

func skillMDBody(name, description, body string) wellKnownEntry {
	return wellKnownEntry{Name: name, Type: "skill-md", Description: description, body: []byte(body), contentType: "text/markdown"}
}

// tarEntry is one member of a test archive. A Linkname makes it a symlink.
type tarEntry struct {
	Name, Body, Linkname string
	Mode                 int64
	// Hard makes a Linkname a hard link.
	Hard bool
}

func tarGzEntry(t *testing.T, name, description string, files ...tarEntry) wellKnownEntry {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		header := &tar.Header{Name: f.Name, Mode: cmpMode(f.Mode), Size: int64(len(f.Body)), Typeflag: tar.TypeReg}
		if f.Linkname != "" {
			header.Typeflag, header.Linkname, header.Size = tar.TypeSymlink, f.Linkname, 0
			if f.Hard {
				header.Typeflag = tar.TypeLink
			}
		}
		if strings.HasSuffix(f.Name, "/") {
			header.Typeflag, header.Size, header.Mode = tar.TypeDir, 0, 0o755
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(f.Body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return wellKnownEntry{Name: name, Type: "archive", Description: description, body: buf.Bytes(), contentType: "application/gzip"}
}

func zipEntry(t *testing.T, name, description string, files ...tarEntry) wellKnownEntry {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		header := &zip.FileHeader{Name: f.Name, Method: zip.Deflate}
		body := f.Body
		if f.Linkname != "" {
			header.SetMode(os.ModeSymlink | 0o777)
			body = f.Linkname
		}
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return wellKnownEntry{Name: name, Type: "archive", Description: description, body: buf.Bytes(), contentType: "application/zip"}
}

func cmpMode(mode int64) int64 {
	if mode == 0 {
		return 0o644
	}
	return mode
}

func skillDoc(name string) string {
	return "---\nname: " + name + "\ndescription: " + name + " skill\n---\n# " + name + "\n"
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
		skillMDEntry("recheck-lint", "Lint Markdown"),
		tarGzEntry(t, "agents-sdk", "Build agents", tarEntry{Name: "SKILL.md", Body: skillDoc("agents-sdk")}),
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
		skillMDEntry("recheck-lint", "Lint Markdown"),
		tarGzEntry(t, "agents-sdk", "Build agents",
			tarEntry{Name: "./"},
			tarEntry{Name: "./SKILL.md", Body: skillDoc("agents-sdk")},
			tarEntry{Name: "./references/api.md", Body: "# API\n"},
			tarEntry{Name: "./scripts/run.sh", Body: "#!/bin/sh\n", Mode: 0o755},
		),
		zipEntry(t, "zipped", "Zipped skill", tarEntry{Name: "SKILL.md", Body: skillDoc("zipped")}, tarEntry{Name: "notes/a.md", Body: "a\n"}),
	)
	scope := newWellKnownScope(t)

	out, err := scope.run(t, "add", server.URL, "--all", "-y")
	if err != nil {
		t.Fatalf("add --all: %v\n%s", err, out)
	}
	for path, want := range map[string]string{
		"agents-sdk/SKILL.md":          skillDoc("agents-sdk"),
		"agents-sdk/references/api.md": "# API\n",
		"recheck-lint/SKILL.md":        string(skillMDEntry("recheck-lint", "Lint Markdown").body),
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
	sum := sha256.Sum256(server.entries[1].body)
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

func TestCLIAddRefusesAWellKnownArtifactItCannotVerify(t *testing.T) {
	zeros := strings.Repeat("\x00", 101<<20)
	cases := []struct {
		name  string
		entry wellKnownEntry
		want  string
	}{
		{"digest mismatch", func() wellKnownEntry {
			e := skillMDEntry("bad", "Bad")
			e.Digest = "sha256:" + strings.Repeat("0", 64)
			return e
		}(), "does not match its digest"},
		{"path traversal", tarGzEntry(t, "bad", "Bad", tarEntry{Name: "SKILL.md", Body: skillDoc("bad")}, tarEntry{Name: "../escape.md", Body: "x"}), "climbs out"},
		{"absolute path", tarGzEntry(t, "bad", "Bad", tarEntry{Name: "SKILL.md", Body: skillDoc("bad")}, tarEntry{Name: "/etc/escape.md", Body: "x"}), "not a relative path"},
		{"symlink", tarGzEntry(t, "bad", "Bad", tarEntry{Name: "SKILL.md", Body: skillDoc("bad")}, tarEntry{Name: "link", Linkname: "SKILL.md"}), "not a regular file"},
		{"hardlink", tarGzEntry(t, "bad", "Bad", tarEntry{Name: "SKILL.md", Body: skillDoc("bad")}, tarEntry{Name: "link", Linkname: "SKILL.md", Hard: true}), "not a regular file"},
		{"zip traversal", zipEntry(t, "bad", "Bad", tarEntry{Name: "SKILL.md", Body: skillDoc("bad")}, tarEntry{Name: "../escape.md", Body: "x"}), "climbs out"},
		{"zip symlink", zipEntry(t, "bad", "Bad", tarEntry{Name: "SKILL.md", Body: skillDoc("bad")}, tarEntry{Name: "link", Linkname: "SKILL.md"}), "not a regular file"},
		{"no SKILL.md", tarGzEntry(t, "bad", "Bad", tarEntry{Name: "nested/SKILL.md", Body: skillDoc("bad")}), "no SKILL.md at the archive root"},
		{"oversized", tarGzEntry(t, "bad", "Bad", tarEntry{Name: "SKILL.md", Body: skillDoc("bad")}, tarEntry{Name: "big.bin", Body: zeros}), "exceeds 100 MiB"},
		{"http redirect", func() wellKnownEntry { e := skillMDEntry("bad", "Bad"); e.URL = "/redirect-http"; return e }(), "only https:// is followed"},
		{"unknown archive", func() wellKnownEntry {
			e := skillMDEntry("bad", "Bad")
			e.Type, e.contentType, e.URL = "archive", "text/plain", "/bad.bin"
			return e
		}(), "neither .tar.gz nor .zip"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newWellKnownServer(t, tc.entry)
			server.extra["/redirect-http"] = func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "http://"+r.Host+"/.well-known/agent-skills/bad/SKILL.md", http.StatusFound)
			}
			scope := newWellKnownScope(t)

			out, err := scope.run(t, "add", server.URL, "--skill", "bad", "-y")
			if ExitCode(err) != 1 || !strings.Contains(out, "Error:") || !strings.Contains(out, tc.want) {
				t.Fatalf("add = %v (exit %d); want exit 1 saying %q\n%s", err, ExitCode(err), tc.want, out)
			}
			if _, err := os.Stat(filepath.Join(scope.skills, "bad")); !os.IsNotExist(err) {
				t.Fatalf("a refused Skill was Materialized: %v", err)
			}
			if _, err := os.Stat(scope.config); !os.IsNotExist(err) {
				t.Fatalf("a refused Skill was declared: %v", err)
			}
			escaped, _ := filepath.Glob(filepath.Join(scope.cache, ".well-known", "*", "escape.md"))
			if len(escaped) > 0 {
				t.Fatalf("an archive member escaped its Skill: %v", escaped)
			}
		})
	}
}

// An archive's format comes from its Content-Type, and from its extension
// only when the type is absent or generic; a redirect to https:// is
// followed.
func TestCLIAddReadsAWellKnownArchiveByContentTypeThenExtension(t *testing.T) {
	zipped := zipEntry(t, "zipped", "Zipped", tarEntry{Name: "SKILL.md", Body: skillDoc("zipped")})
	zipped.URL = "/.well-known/agent-skills/zipped.tar.gz"
	generic := tarGzEntry(t, "generic", "Generic", tarEntry{Name: "SKILL.md", Body: skillDoc("generic")})
	generic.contentType = "application/octet-stream"
	moved := skillMDEntry("moved", "Moved")
	moved.URL = "/redirect-https"
	server := newWellKnownServer(t, zipped, generic, moved)
	server.extra["/redirect-https"] = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/.well-known/agent-skills/moved/SKILL.md", http.StatusFound)
	}
	server.extra["/.well-known/agent-skills/moved/SKILL.md"] = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(moved.body)
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

func TestCLIAddRefusesWellKnownSourcesItDoesNotRead(t *testing.T) {
	server := newWellKnownServer(t, skillMDEntry("one", "One"))
	scope := newWellKnownScope(t)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"path", []string{"add", server.URL, "--path", "skills", "--list"}, "--path does not apply"},
		{"branch", []string{"add", server.URL, "--branch", "main", "-y"}, "--branch does not apply"},
		{"http", []string{"add", strings.Replace(server.URL, "https://", "http://", 1), "--list"}, "https://"},
	} {
		out, err := scope.run(t, tc.args...)
		if ExitCode(err) != 1 || !strings.Contains(out, tc.want) {
			t.Errorf("%s: add = %v (exit %d); want exit 1 saying %q\n%s", tc.name, err, ExitCode(err), tc.want, out)
		}
	}

	server.schema = ""
	out, err := scope.run(t, "add", server.URL, "--list")
	if ExitCode(err) != 1 || !strings.Contains(out, "v0.1.0") || !strings.Contains(out, "git repository") {
		t.Fatalf("v0.1.0 index: add = %v (exit %d); want exit 1 naming the git Source\n%s", err, ExitCode(err), out)
	}
}

func TestCLIAddSkipsWellKnownEntriesOfAnUnknownType(t *testing.T) {
	odd := skillMDEntry("odd", "Odd")
	odd.Type = "bundle"
	server := newWellKnownServer(t, skillMDEntry("one", "One"), odd)
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
	server := newWellKnownServer(t, skillMDEntry("one", "One"), skillMDEntry("other", "Other"))
	scope := newWellKnownScope(t)
	if out, err := scope.run(t, "add", server.URL, "--skill", "one", "-y"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	server.requested("")

	// An unchanged index is revalidated, whatever its max-age says, and
	// answered 304.
	out, err := scope.run(t, "outdated")
	if err != nil {
		t.Fatalf("outdated on an unchanged index = %v (exit %d)\n%s", err, ExitCode(err), out)
	}
	if got := server.requested("/"); len(got) != 1 || got[0] != "GET /.well-known/agent-skills/index.json 304" {
		t.Fatalf("outdated requests = %v; want one conditional index request answered 304", got)
	}

	// A new digest is outdated, and update brings it in.
	server.setEntries(skillMDBody("one", "One", "# one, revised\n"), skillMDEntry("other", "Other, revised"))
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
	for _, r := range server.requested("/.well-known/agent-skills/other") {
		t.Errorf("update downloaded an undeclared Skill: %s", r)
	}
	applied, _ := engine.OpenBaselines(scope.skills).Applied("one")
	if applied.AppliedCommit != server.entries[0].Digest {
		t.Fatalf("Baseline commit = %s; want %s", applied.AppliedCommit, server.entries[0].Digest)
	}
	if out, err := scope.run(t, "outdated"); err != nil {
		t.Fatalf("outdated after update = %v (exit %d)\n%s", err, ExitCode(err), out)
	}
}

func TestCLIUpdateProtectsADriftedWellKnownSkillAndDiffShowsTheChange(t *testing.T) {
	server := newWellKnownServer(t, skillMDBody("one", "One", "line one\n"))
	scope := newWellKnownScope(t)
	if out, err := scope.run(t, "add", server.URL, "--all", "-y"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	server.setEntries(skillMDBody("one", "One", "line one\nline two\n"))

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
	server := newWellKnownServer(t, skillMDEntry("old", "Old"), skillMDEntry("gone", "Gone"), skillMDEntry("bystander", "Bystander"))
	scope := newWellKnownScope(t)
	if out, err := scope.run(t, "add", server.URL, "--skill", "old", "--skill", "gone", "-y"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	successor := skillMDBody("new", "New", "---\nname: new\ndescription: New\nmetadata:\n  replaces: old\n---\n# new\n")
	server.setEntries(successor, skillMDEntry("bystander", "Bystander"))

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
	server.setEntries(successor, skillMDBody("bystander", "Bystander", "# changed\n"))
	server.requested("")
	if out, err := scope.run(t, "update"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	for _, r := range server.requested("/.well-known/agent-skills/bystander") {
		t.Errorf("update downloaded an undeclared Skill with nothing to rename: %s", r)
	}
}
