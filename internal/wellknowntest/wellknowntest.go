// Package wellknowntest serves an Agent Skills Discovery index over TLS for
// tests of well-known Sources. It does not import the engine, so the engine's
// own tests can use it; a caller points the engine's HTTP client at
// Server.Client.
package wellknowntest

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
	"strings"
	"sync"
	"testing"
)

// Entry is one artifact a Server serves, and the index entry naming it.
type Entry struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Digest      string `json:"digest"`

	Body        []byte `json:"-"`
	ContentType string `json:"-"`
}

// Server is a publisher of an Agent Skills Discovery index over TLS. Its
// index and artifacts may change between requests.
type Server struct {
	*httptest.Server
	mu      sync.Mutex
	Schema  string
	Entries []Entry
	// Extra serves additional paths, such as a redirect.
	Extra map[string]http.HandlerFunc
	// requests logs each request as "<method> <path> <status>".
	requests []string
}

// Requested is the logged requests whose path has prefix, then clears the log.
func (s *Server) Requested(prefix string) []string {
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

// SchemaV2 is the index version well-known Sources are read at.
const SchemaV2 = "https://schemas.agentskills.io/discovery/0.2.0/schema.json"

// NewServer starts a Server publishing entries, closed when t ends.
func NewServer(t *testing.T, entries ...Entry) *Server {
	t.Helper()
	s := &Server{Schema: SchemaV2, Extra: map[string]http.HandlerFunc{}}
	s.SetEntries(entries...)
	s.Server = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// SetEntries replaces what the server publishes. An entry with no Digest
// gets the digest of its body; one with no URL is served at the conventional
// path.
func (s *Server) SetEntries(entries ...Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range entries {
		if entries[i].Digest == "" {
			sum := sha256.Sum256(entries[i].Body)
			entries[i].Digest = "sha256:" + hex.EncodeToString(sum[:])
		}
		if entries[i].URL == "" {
			entries[i].URL = "/.well-known/agent-skills/" + entries[i].Name + artifactExtension(entries[i])
		}
	}
	s.Entries = entries
}

func artifactExtension(entry Entry) string {
	switch {
	case entry.Type == "skill-md":
		return "/SKILL.md"
	case entry.ContentType == "application/zip":
		return ".zip"
	default:
		return ".tar.gz"
	}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if handler, ok := s.Extra[r.URL.Path]; ok {
		handler(w, r)
		return
	}
	if r.URL.Path == "/.well-known/agent-skills/index.json" {
		index := map[string]any{"skills": s.Entries}
		if s.Schema != "" {
			index["$schema"] = s.Schema
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
	for _, entry := range s.Entries {
		if entry.URL == r.URL.Path {
			if entry.ContentType != "" {
				w.Header().Set("Content-Type", entry.ContentType)
			}
			_, _ = w.Write(entry.Body)
			return
		}
	}
	http.NotFound(w, r)
}

// SkillMD is a single SKILL.md entry with frontmatter naming it.
func SkillMD(name, description string) Entry {
	return SkillMDBody(name, description, "---\nname: "+name+"\ndescription: "+description+"\n---\n# "+name+"\n")
}

// SkillMDBody is a single SKILL.md entry holding body.
func SkillMDBody(name, description, body string) Entry {
	return Entry{Name: name, Type: "skill-md", Description: description, Body: []byte(body), ContentType: "text/markdown"}
}

// File is one member of a test archive. A Linkname makes it a symlink.
type File struct {
	Name, Body, Linkname string
	Mode                 int64
	// Hard makes a Linkname a hard link.
	Hard bool
}

// TarGz is an archive entry served as a gzipped tarball of files.
func TarGz(t *testing.T, name, description string, files ...File) Entry {
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
	return Entry{Name: name, Type: "archive", Description: description, Body: buf.Bytes(), ContentType: "application/gzip"}
}

// Zip is an archive entry served as a zip of files.
func Zip(t *testing.T, name, description string, files ...File) Entry {
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
	return Entry{Name: name, Type: "archive", Description: description, Body: buf.Bytes(), ContentType: "application/zip"}
}

func cmpMode(mode int64) int64 {
	if mode == 0 {
		return 0o644
	}
	return mode
}

// SkillDoc is a SKILL.md naming name.
func SkillDoc(name string) string {
	return "---\nname: " + name + "\ndescription: " + name + " skill\n---\n# " + name + "\n"
}
