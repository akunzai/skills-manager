package models

import (
	"os"
	"path/filepath"
	"testing"
)

// ParseSource decides what a raw Source string names in one order: a local
// path, then anything on the Gist host, then a well-known site or index,
// then a git repository.
func TestParseSource(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		raw     string
		kind    SourceKind
		key     string
		refused bool
	}{
		{raw: "/abs/skills", kind: SourceLocal},
		{raw: "~/skills", kind: SourceLocal},
		{raw: "./skills", kind: SourceLocal},
		{raw: `..\skills`, kind: SourceLocal},
		{raw: `C:\skills`, kind: SourceLocal},
		{raw: "owner/repo", kind: SourceGit, key: "owner/repo"},
		{raw: "owner/repo/skills", kind: SourceGit, key: "owner/repo"},
		{raw: "github:owner/repo", kind: SourceGit, key: "owner/repo"},
		{raw: "gitlab:group/project", kind: SourceGit, key: "gitlab.com/group/project"},
		{raw: "https://github.com/owner/repo", kind: SourceGit, key: "owner/repo"},
		{raw: "git@github.com:owner/repo.git", kind: SourceGit, key: "owner/repo"},
		{raw: "gist:" + id, kind: SourceGit, key: "gist.github.com/" + id},
		{raw: "https://gist.github.com/" + id, kind: SourceGit, key: "gist.github.com/" + id},
		{raw: "https://gist.github.com/someone/" + id, kind: SourceGit, key: "gist.github.com/" + id},
		{raw: "HTTPS://GIST.GITHUB.COM/" + id, kind: SourceGit, key: "gist.github.com/" + id},
		{raw: "git@gist.github.com:" + id + ".git", kind: SourceGit, key: "gist.github.com/" + id},
		// Anything on the Gist host is a Gist or refused, never a
		// well-known site, and never a GitHub owner.
		{raw: "https://gist.github.com/", refused: true},
		{raw: "https://gist.github.com", refused: true},
		{raw: "http://gist.github.com/", refused: true},
		{raw: "https://gist.github.com/someone", refused: true},
		{raw: "https://gist.github.com/someone/" + id + "/raw/SKILL.md", refused: true},
		{raw: "gist:", refused: true},
		{raw: "https://example.com", kind: SourceWellKnown, key: "example.com"},
		{raw: "https://example.com/docs/.well-known/agent-skills/index.json", kind: SourceWellKnown, key: "example.com/docs"},
		{raw: "https://example.com/owner/repo", kind: SourceGit, key: "example.com/owner/repo"},
	} {
		got, err := ParseSource(tc.raw)
		if tc.refused {
			if err == nil {
				t.Errorf("ParseSource(%q) = %+v; want a refusal", tc.raw, got)
			}
			continue
		}
		if err != nil || got.Kind != tc.kind || got.Key() != tc.key {
			t.Errorf("ParseSource(%q) = %s %q, %v; want %s %q", tc.raw, got.Kind, got.Key(), err, tc.kind, tc.key)
		}
	}
}

// A bare name is a local path only when a directory by that name exists.
func TestParseSourceTakesAnExistingDirectoryForLocal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(dir, "owner", "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := ParseSource("owner/repo"); err != nil || got.Kind != SourceLocal {
		t.Fatalf("ParseSource(owner/repo) = %+v, %v; want local, since the directory exists", got, err)
	}
	if got, err := ParseRemoteSource("owner/repo"); err != nil || got.Kind != SourceGit || got.Key() != "owner/repo" {
		t.Fatalf("ParseRemoteSource(owner/repo) = %+v, %v; want the git Source, whatever is on disk", got, err)
	}
}
