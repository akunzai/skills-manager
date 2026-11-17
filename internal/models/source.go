package models

import (
	"os"
	"strings"
)

// SourceKind is what a raw Source string names.
type SourceKind string

const (
	// SourceLocal is a directory on this machine.
	SourceLocal SourceKind = "local"
	// SourceWellKnown is a site, or its index, serving an Agent Skills
	// Discovery index.
	SourceWellKnown SourceKind = "well-known"
	// SourceGit is a git repository, a GitHub Gist included.
	SourceGit SourceKind = "git"
)

// Source is a raw Source string parsed once: its kind, and the parse that
// kind reads.
type Source struct {
	Kind SourceKind
	// Repo is a git Source's key, clone URL, type, branch, and subpath.
	Repo ParsedRepoSource
	// WellKnown is a well-known Source's key and base URL.
	WellKnown WellKnownSource
}

// Key is the Source key: empty for a local path.
func (s Source) Key() string {
	switch s.Kind {
	case SourceGit:
		return s.Repo.SourceKey
	case SourceWellKnown:
		return s.WellKnown.SourceKey
	}
	return ""
}

// ParseSource is the one parse of a raw Source string a user or an
// installer lock names; a Source key already declared is read with
// ParseRepoSource. It decides what raw names, in one order: a local path, then
// anything on the Gist host, then a well-known site or index, then a git
// repository. The error is a form this tool refuses, such as a Gist URL
// naming a revision or no Gist at all; it is never read as another kind.
func ParseSource(raw string) (Source, error) {
	if isLocalPath(raw) {
		return Source{Kind: SourceLocal}, nil
	}
	return ParseRemoteSource(raw)
}

// ParseRemoteSource is ParseSource for a string known to name no local
// path, such as an installer lock record: nothing on disk is consulted.
func ParseRemoteSource(raw string) (Source, error) {
	if gist, ok, err := ParseGistSource(raw); ok {
		return Source{Kind: SourceGit, Repo: gist}, err
	}
	if wellKnown, ok := ParseWellKnownSource(raw); ok {
		return Source{Kind: SourceWellKnown, WellKnown: wellKnown}, nil
	}
	return Source{Kind: SourceGit, Repo: ParseRepoSource(raw)}, nil
}

// isLocalPath is whether raw names a local directory: an absolute, home, or
// relative path, or a bare name that is not a remote form and exists as a
// directory.
func isLocalPath(raw string) bool {
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "~") ||
		strings.HasPrefix(raw, "./") || strings.HasPrefix(raw, "../") ||
		strings.HasPrefix(raw, `.\`) || strings.HasPrefix(raw, `..\`) {
		return true
	}
	if len(raw) >= 3 && ((raw[0] >= 'a' && raw[0] <= 'z') || (raw[0] >= 'A' && raw[0] <= 'Z')) && raw[1] == ':' && (raw[2] == '/' || raw[2] == '\\') {
		return true
	}
	if !strings.HasPrefix(raw, "git@") && !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") &&
		!strings.HasPrefix(raw, "github:") && !strings.HasPrefix(raw, "gitlab:") && !strings.HasPrefix(raw, "gist:") {
		if info, err := os.Stat(ExpandUser(raw)); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}
