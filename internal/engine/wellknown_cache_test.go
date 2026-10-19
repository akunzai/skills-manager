package engine

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/wellknowntest"
)

// newWellKnownTestCache publishes entries over TLS and opens a Cache of
// them, pointing the well-known client at the server for the rest of the
// test.
func newWellKnownTestCache(t *testing.T, entries ...wellknowntest.Entry) (*wellknowntest.Server, WellKnownCache, string) {
	t.Helper()
	server := wellknowntest.NewServer(t, entries...)
	previous := WellKnownHTTPClient
	WellKnownHTTPClient = server.Client()
	t.Cleanup(func() { WellKnownHTTPClient = previous })
	cacheDir := filepath.Join(t.TempDir(), "cache")
	return server, NewWellKnownCache("example.com", server.URL, cacheDir), cacheDir
}

func TestWellKnownCacheRefusesAnArtifactItCannotVerify(t *testing.T) {
	skillMD := wellknowntest.File{Name: "SKILL.md", Body: wellknowntest.SkillDoc("bad")}
	zeros := strings.Repeat("\x00", 101<<20)
	cases := []struct {
		name  string
		entry wellknowntest.Entry
		want  string
	}{
		{"digest mismatch", func() wellknowntest.Entry {
			e := wellknowntest.SkillMD("bad", "Bad")
			e.Digest = "sha256:" + strings.Repeat("0", 64)
			return e
		}(), "does not match its digest"},
		{"path traversal", wellknowntest.TarGz(t, "bad", "Bad", skillMD, wellknowntest.File{Name: "../escape.md", Body: "x"}), "climbs out"},
		{"absolute path", wellknowntest.TarGz(t, "bad", "Bad", skillMD, wellknowntest.File{Name: "/etc/escape.md", Body: "x"}), "not a relative path"},
		{"symlink", wellknowntest.TarGz(t, "bad", "Bad", skillMD, wellknowntest.File{Name: "link", Linkname: "SKILL.md"}), "not a regular file"},
		{"hardlink", wellknowntest.TarGz(t, "bad", "Bad", skillMD, wellknowntest.File{Name: "link", Linkname: "SKILL.md", Hard: true}), "not a regular file"},
		{"zip traversal", wellknowntest.Zip(t, "bad", "Bad", skillMD, wellknowntest.File{Name: "../escape.md", Body: "x"}), "climbs out"},
		{"zip symlink", wellknowntest.Zip(t, "bad", "Bad", skillMD, wellknowntest.File{Name: "link", Linkname: "SKILL.md"}), "not a regular file"},
		{"no SKILL.md", wellknowntest.TarGz(t, "bad", "Bad", wellknowntest.File{Name: "nested/SKILL.md", Body: wellknowntest.SkillDoc("bad")}), "no SKILL.md at the archive root"},
		{"oversized", wellknowntest.TarGz(t, "bad", "Bad", skillMD, wellknowntest.File{Name: "big.bin", Body: zeros}), "exceeds 100 MiB"},
		{"http redirect", func() wellknowntest.Entry {
			e := wellknowntest.SkillMD("bad", "Bad")
			e.URL = "/redirect-http"
			return e
		}(), "only https:// is followed"},
		{"unknown archive", func() wellknowntest.Entry {
			e := wellknowntest.SkillMD("bad", "Bad")
			e.Type, e.ContentType, e.URL = "archive", "text/plain", "/bad.bin"
			return e
		}(), "neither .tar.gz nor .zip"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, cache, cacheDir := newWellKnownTestCache(t, tc.entry)
			server.Extra["/redirect-http"] = func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "http://"+r.Host+"/.well-known/agent-skills/bad/SKILL.md", http.StatusFound)
			}
			if err := cache.RefreshWithProgress(true, nil); err != nil {
				t.Fatal(err)
			}

			err := cache.Cover("bad")
			if err == nil || !IsWellKnownRefusal(err) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Cover(bad) = %v; want a refusal saying %q", err, tc.want)
			}
			if left, _ := os.ReadDir(cache.dir()); len(left) > 0 {
				t.Fatalf("the Cache holds %v after a refusal; want nothing", left)
			}
			if staging, _ := filepath.Glob(filepath.Join(cache.root(), ".fetch-*")); len(staging) > 0 {
				t.Fatalf("a refused artifact left its staging behind: %v", staging)
			}
			if coverage, _ := cache.coverage(); len(coverage.missing([]string{"bad"})) == 0 {
				t.Fatalf("coverage() = %+v; want a refused Skill not covered", coverage)
			}
			var escaped []string
			err = filepath.WalkDir(filepath.Dir(cacheDir), func(path string, _ os.DirEntry, err error) error {
				if filepath.Base(path) == "escape.md" {
					escaped = append(escaped, path)
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(escaped) > 0 {
				t.Fatalf("an archive member escaped its Skill: %v", escaped)
			}
		})
	}
}

func TestWellKnownCacheRevalidatesItsIndexAndDropsWhatTheIndexNoLongerLists(t *testing.T) {
	server, cache, _ := newWellKnownTestCache(t, wellknowntest.SkillMD("one", "One"), wellknowntest.SkillMD("gone", "Gone"))
	if err := cache.RefreshWithProgress(true, nil, "one", "gone"); err != nil {
		t.Fatal(err)
	}
	head := cache.head()
	server.Requested("")

	// An unchanged index is asked about, whatever its max-age says, and
	// confirmed with a 304 that leaves the Cache as it was.
	if err := cache.RefreshWithProgress(false, nil, "one"); err != nil {
		t.Fatal(err)
	}
	if got := server.Requested("/"); len(got) != 1 || got[0] != "GET /.well-known/agent-skills/index.json 304" {
		t.Fatalf("requests = %v; want one conditional index request answered 304", got)
	}
	if cache.head() != head {
		t.Fatalf("head() = %q after a 304; want %q", cache.head(), head)
	}

	// A changed index replaces the cached one: the Skill it changed is
	// covered at its new digest, and the one it dropped leaves the Cache.
	server.SetEntries(wellknowntest.SkillMDBody("one", "One", "# one, revised\n"))
	if err := cache.RefreshWithProgress(false, nil, "one"); err != nil {
		t.Fatal(err)
	}
	if got, want := cache.atHead("one").id, server.Entries[0].Digest; got != want {
		t.Fatalf("atHead(one) = %q; want the new digest %q", got, want)
	}
	if data, _ := os.ReadFile(filepath.Join(cache.dir(), "one", "SKILL.md")); string(data) != "# one, revised\n" {
		t.Fatalf("one/SKILL.md = %q; want the revised one", data)
	}
	if cache.atHead("gone").exists() {
		t.Fatal("atHead(gone) exists after the index dropped it")
	}
	if coverage, _ := cache.coverage(); len(coverage.missing([]string{"gone"})) == 0 {
		t.Fatalf("coverage() = %+v; want gone no longer covered", coverage)
	}
	if _, err := os.Stat(filepath.Join(cache.dir(), "gone")); !os.IsNotExist(err) {
		t.Fatalf("gone is still in the Cache: %v", err)
	}
}
