package engine

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	neturl "net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/akunzai/skills-manager/internal/models"
)

// WellKnownHTTPClient fetches well-known indexes and their artifacts. Tests
// replace it with an httptest server's client; they must not reach the
// network. Whatever client is set, a redirect is followed only to https://.
var WellKnownHTTPClient = &http.Client{Timeout: 5 * time.Minute}

// wellKnownSchemaV2 is the only index version Add reads: v0.2.0, whose
// entries carry a digest. An index without $schema is v0.1.0, which has none
// (https://github.com/cloudflare/agent-skills-discovery-rfc#versioning).
const wellKnownSchemaV2 = "https://schemas.agentskills.io/discovery/0.2.0/schema.json"

const (
	// wellKnownIndexLimit bounds the index read.
	wellKnownIndexLimit = 10 << 20
	// wellKnownArtifactLimit bounds an artifact as downloaded, and a
	// Skill's files once unpacked, against a decompression bomb.
	wellKnownArtifactLimit = 100 << 20
)

// wellKnownName is the Agent Skills name rule an index entry must follow:
// lowercase alphanumerics and single hyphens, 1-64 characters. It also keeps
// the name safe as a directory name.
var wellKnownName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

var wellKnownDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// wellKnownEntry is one Skill in a well-known index. Unknown fields are
// ignored, as the RFC requires.
type wellKnownEntry struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Digest      string `json:"digest"`
}

type wellKnownIndex struct {
	Schema *string          `json:"$schema"`
	Skills []wellKnownEntry `json:"skills"`
}

// wellKnownState is what a WellKnownCache knows beyond the index: the digest
// of the index it holds, and of the artifact each Skill on disk was unpacked
// from.
type wellKnownState struct {
	Index   string            `json:"index"`
	Covered map[string]string `json:"covered,omitempty"`
}

// WellKnownCache is one well-known Source's Cache: its last index, and the
// Skills a Scope declares from it, each unpacked from an artifact verified
// against the digest the index gave. Every Scope shares it. It reads no
// Config and reaches the network only to refresh or cover.
type WellKnownCache struct {
	source   string
	base     string
	cacheDir string
}

func NewWellKnownCache(source, base, cacheDir string) WellKnownCache {
	return WellKnownCache{source: source, base: strings.TrimRight(base, "/"), cacheDir: cacheDirOrDefault(cacheDir)}
}

// root holds this Source's Cache. A Source key cannot start with a dot, so
// .well-known never collides with a git Cache; a base URL with a path gets a
// digest of it, so one base's files never land in another's.
func (c WellKnownCache) root() string {
	u, err := neturl.Parse(c.base)
	host := c.source
	prefix := ""
	if err == nil {
		host, prefix = strings.ToLower(u.Host), u.Path
	}
	name := strings.ReplaceAll(host, ":", "_")
	if prefix != "" {
		sum := sha256.Sum256([]byte(prefix))
		name += "~" + hex.EncodeToString(sum[:])[:12]
	}
	return filepath.Join(c.cacheDir, ".well-known", name)
}

func (c WellKnownCache) dir() string { return filepath.Join(c.root(), "skills") }

func (c WellKnownCache) indexPath() string { return filepath.Join(c.root(), "index.json") }

func (c WellKnownCache) statePath() string { return filepath.Join(c.root(), "state.json") }

func (c WellKnownCache) indexURL() string { return models.WellKnownIndexURL(c.base) }

func (c WellKnownCache) state() wellKnownState {
	var state wellKnownState
	if data, err := os.ReadFile(c.statePath()); err == nil {
		_ = json.Unmarshal(data, &state)
	}
	return state
}

func (c WellKnownCache) saveState(state wellKnownState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(c.statePath(), append(data, '\n'))
}

// index is the usable entries of the index this Cache holds, by name, or
// ok=false when there is none.
func (c WellKnownCache) index() (entries map[string]wellKnownEntry, ok bool) {
	usable, _, err := c.wellKnownEntries()
	if err != nil {
		return nil, false
	}
	entries = make(map[string]wellKnownEntry, len(usable))
	for _, entry := range usable {
		entries[entry.Name] = entry
	}
	return entries, true
}

func (c WellKnownCache) head() string {
	if _, ok := c.index(); !ok {
		return ""
	}
	return c.state().Index
}

// atHead is the index entry named subpath. Its id is the digest of the
// artifact on disk once covered, else the one the index names.
func (c WellKnownCache) atHead(subpath string) headEntry {
	entries, _ := c.index()
	entry, ok := entries[subpath]
	if !ok {
		return headEntry{}
	}
	if covered, ok := c.state().Covered[subpath]; ok {
		return headEntry{id: covered, dir: true}
	}
	return headEntry{id: entry.Digest, dir: true}
}

// coverage covers the Skills unpacked on disk.
func (c WellKnownCache) coverage() (sparseState, error) {
	state := sparseState{enabled: true, cone: true}
	for _, name := range slices.Sorted(maps.Keys(c.state().Covered)) {
		if info, err := os.Stat(filepath.Join(c.dir(), name)); err == nil && info.IsDir() {
			state.dirs = append(state.dirs, name)
		}
	}
	return state, nil
}

func (c WellKnownCache) identity() string { return c.base }

func (c WellKnownCache) appliedCommit(subpath string) string {
	return c.state().Covered[subpath]
}

func (c WellKnownCache) where() string { return "" }

func (c WellKnownCache) observe(...string) cacheFacts {
	return cacheFacts{source: c.source, url: c.base, dir: c.dir(), localSHA: c.head(), cache: c, err: "observing a well-known Source is not supported yet"}
}

func (c WellKnownCache) withSkillFiles(fn func(repoDir string) error) error {
	return fn(c.dir())
}

// Refresh fetches the index and covers paths.
func (c WellKnownCache) Refresh(force bool, paths ...string) error {
	return c.RefreshWithProgress(force, nil, paths...)
}

func (c WellKnownCache) RefreshWithProgress(_ bool, _ UpdateProgress, paths ...string) error {
	body, err := wellKnownGet(c.indexURL(), wellKnownIndexLimit)
	if err != nil {
		return fmt.Errorf("fetch index of %s: %w", c.source, err)
	}
	if err := parseWellKnownIndex(c.indexURL(), body.data); err != nil {
		return err
	}
	if err := writeFileAtomic(c.indexPath(), body.data); err != nil {
		return err
	}
	state := c.state()
	sum := sha256.Sum256(body.data)
	state.Index = "sha256:" + hex.EncodeToString(sum[:])
	if err := c.saveState(state); err != nil {
		return err
	}
	return c.Cover(paths...)
}

// Cover downloads, verifies, and unpacks each entry of paths the index names
// whose artifact on disk is not the one the index names. An entry the index
// does not name is left for Freshness to report.
func (c WellKnownCache) Cover(paths ...string) error {
	entries, ok := c.index()
	if !ok {
		return fmt.Errorf("no index cached for %s", c.source)
	}
	state := c.state()
	for _, name := range paths {
		entry, ok := entries[name]
		if !ok {
			continue
		}
		if state.Covered[name] == entry.Digest {
			if info, err := os.Stat(filepath.Join(c.dir(), name)); err == nil && info.IsDir() {
				continue
			}
		}
		if err := c.fetchEntry(entry); err != nil {
			return fmt.Errorf("fetch Skill %s from %s: %w", name, c.source, err)
		}
		if state.Covered == nil {
			state.Covered = make(map[string]string)
		}
		state.Covered[name] = entry.Digest
		if err := c.saveState(state); err != nil {
			return err
		}
	}
	return nil
}

// fetchEntry replaces the entry's Skill on disk with its artifact, once the
// artifact matches the entry's digest and, for an archive, unpacks safely.
func (c WellKnownCache) fetchEntry(entry wellKnownEntry) error {
	artifactURL, err := resolveWellKnownURL(c.indexURL(), entry.URL)
	if err != nil {
		return err
	}
	body, err := wellKnownGet(artifactURL, wellKnownArtifactLimit)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body.data)
	if got := "sha256:" + hex.EncodeToString(sum[:]); got != entry.Digest {
		return refuse("artifact %s does not match its digest: index names %s, got %s", artifactURL, entry.Digest, got)
	}
	if err := os.MkdirAll(c.dir(), 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(c.root(), ".fetch-"+entry.Name+"-")
	if err != nil {
		return err
	}
	defer func() { _ = RemoveAll(staging) }()
	switch entry.Type {
	case "skill-md":
		err = os.WriteFile(filepath.Join(staging, "SKILL.md"), body.data, 0o644)
	case "archive":
		err = unpackWellKnownArchive(staging, body, artifactURL)
	}
	if err != nil {
		return err
	}
	target := filepath.Join(c.dir(), entry.Name)
	if err := RemoveAll(target); err != nil {
		return err
	}
	return os.Rename(staging, target)
}

// usableWellKnownEntry is why an index entry cannot be used, or nil.
func usableWellKnownEntry(entry wellKnownEntry) error {
	switch {
	case len(entry.Name) > 64 || !wellKnownName.MatchString(entry.Name):
		return fmt.Errorf("Skill name %q is not a valid Agent Skills name", entry.Name)
	case entry.Type != "skill-md" && entry.Type != "archive":
		return fmt.Errorf("Skill %s has unknown type %q", entry.Name, entry.Type)
	case !wellKnownDigest.MatchString(entry.Digest):
		return fmt.Errorf("Skill %s has no sha256 digest", entry.Name)
	case entry.URL == "":
		return fmt.Errorf("Skill %s has no url", entry.Name)
	}
	return nil
}

// refusalError is something a well-known Source serves that this tool will
// not use: an index or artifact that cannot be verified or unpacked safely.
// It is the Source's state for the user to resolve, not a failed operation
// (ADR-0002).
type refusalError struct{ err error }

func (e refusalError) Error() string { return e.err.Error() }
func (e refusalError) Unwrap() error { return e.err }

func refuse(format string, args ...any) error {
	return refusalError{fmt.Errorf(format, args...)}
}

// IsWellKnownRefusal reports whether err is a well-known index or artifact
// refused because it cannot be verified or unpacked safely.
func IsWellKnownRefusal(err error) bool {
	_, ok := errors.AsType[refusalError](err)
	return ok
}

// errWellKnownV1 is an index with no $schema: v0.1.0, which carries no
// digests, so nothing it names can be verified.
var errWellKnownV1 = errors.New("is an Agent Skills Discovery v0.1.0 index, which carries no digests to verify its Skills against")

// parseWellKnownIndex reads a v0.2.0 index, refusing any other version.
func parseWellKnownIndex(indexURL string, data []byte) error {
	var index wellKnownIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return fmt.Errorf("read index %s: %w", indexURL, err)
	}
	switch {
	case index.Schema == nil:
		return refuse("%s %w", indexURL, errWellKnownV1)
	case *index.Schema != wellKnownSchemaV2:
		return refuse("%s names an unrecognized $schema %q; only %s is supported", indexURL, *index.Schema, wellKnownSchemaV2)
	}
	return nil
}

// IsWellKnownV1 reports whether err is an index Add refused as v0.1.0.
func IsWellKnownV1(err error) bool { return errors.Is(err, errWellKnownV1) }

// wellKnownEntries is the usable entries of the index this Cache holds, and
// why each other one was skipped.
func (c WellKnownCache) wellKnownEntries() ([]wellKnownEntry, []string, error) {
	data, err := os.ReadFile(c.indexPath())
	if err != nil {
		return nil, nil, err
	}
	var index wellKnownIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, nil, err
	}
	var usable []wellKnownEntry
	var skipped []string
	seen := make(map[string]bool)
	for _, entry := range index.Skills {
		if err := usableWellKnownEntry(entry); err != nil {
			skipped = append(skipped, err.Error()+"; skipped")
			continue
		}
		if seen[entry.Name] {
			skipped = append(skipped, fmt.Sprintf("Skill %s is listed more than once; the first is used", entry.Name))
			continue
		}
		seen[entry.Name] = true
		usable = append(usable, entry)
	}
	return usable, skipped, nil
}

// resolveWellKnownURL resolves an entry url against the index URL (RFC 3986
// §5). The artifact may sit on another host, but only over https.
func resolveWellKnownURL(indexURL, ref string) (string, error) {
	base, err := neturl.Parse(indexURL)
	if err != nil {
		return "", err
	}
	target, err := base.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("invalid url %q: %w", ref, err)
	}
	if target.Scheme != "https" {
		return "", refuse("refusing %s: only https:// is fetched", target.Redacted())
	}
	return target.String(), nil
}

type wellKnownBody struct {
	data        []byte
	contentType string
}

// wellKnownGet fetches rawURL, refusing a body over limit bytes and a
// redirect anywhere but https://.
func wellKnownGet(rawURL string, limit int64) (wellKnownBody, error) {
	client := *WellKnownHTTPClient
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return refuse("refusing redirect to %s: only https:// is followed", req.URL.Redacted())
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	resp, err := client.Get(rawURL)
	if err != nil {
		return wellKnownBody{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return wellKnownBody{}, fmt.Errorf("GET %s: %s", rawURL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return wellKnownBody{}, err
	}
	if int64(len(data)) > limit {
		return wellKnownBody{}, refuse("GET %s: larger than %d MiB", rawURL, limit>>20)
	}
	return wellKnownBody{data: data, contentType: resp.Header.Get("Content-Type")}, nil
}

// archiveFormat is "tar.gz" or "zip": from the Content-Type, else, when it
// is absent or generic, from the URL's extension.
func archiveFormat(contentType, artifactURL string) (string, error) {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	switch mediaType {
	case "application/gzip", "application/x-gzip", "application/x-tar+gzip", "application/x-compressed-tar":
		return "tar.gz", nil
	case "application/zip", "application/x-zip-compressed":
		return "zip", nil
	}
	p := artifactURL
	if u, err := neturl.Parse(artifactURL); err == nil {
		p = u.Path
	}
	switch {
	case strings.HasSuffix(p, ".tar.gz"), strings.HasSuffix(p, ".tgz"):
		return "tar.gz", nil
	case strings.HasSuffix(p, ".zip"):
		return "zip", nil
	}
	return "", refuse("archive %s is neither .tar.gz nor .zip (Content-Type %q)", artifactURL, contentType)
}

// unpackWellKnownArchive unpacks an archive into dir, refusing it unless
// every member is a regular file or directory under the root, SKILL.md is at
// the root, and the files stay within wellKnownArtifactLimit
// (https://github.com/cloudflare/agent-skills-discovery-rfc#archive-safety).
func unpackWellKnownArchive(dir string, body wellKnownBody, artifactURL string) error {
	format, err := archiveFormat(body.contentType, artifactURL)
	if err != nil {
		return err
	}
	u := &unpacker{dir: dir}
	if format == "zip" {
		err = u.zip(body.data)
	} else {
		err = u.tarGz(body.data)
	}
	if err != nil {
		return refuse("unpack %s: %w", artifactURL, err)
	}
	if info, err := os.Lstat(filepath.Join(dir, "SKILL.md")); err != nil || !info.Mode().IsRegular() {
		return refuse("unpack %s: no SKILL.md at the archive root", artifactURL)
	}
	return nil
}

type unpacker struct {
	dir     string
	written int64
}

// target is where member name goes, or an error when it would leave dir.
// "" is the root itself.
func (u *unpacker) target(name string) (string, error) {
	if strings.Contains(name, `\`) || path.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", fmt.Errorf("member %q is not a relative path", name)
	}
	for part := range strings.SplitSeq(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("member %q climbs out of the archive", name)
		}
	}
	clean := path.Clean(name)
	if clean == "." {
		return "", nil
	}
	return filepath.Join(u.dir, filepath.FromSlash(clean)), nil
}

func (u *unpacker) mkdir(name string) error {
	target, err := u.target(name)
	if err != nil || target == "" {
		return err
	}
	return os.MkdirAll(target, 0o755)
}

func (u *unpacker) file(name string, mode os.FileMode, r io.Reader) error {
	target, err := u.target(name)
	if err != nil {
		return err
	}
	if target == "" {
		return fmt.Errorf("member %q is not a file", name)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	perm := os.FileMode(0o644)
	if mode&0o111 != 0 {
		perm = 0o755
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, wellKnownArtifactLimit-u.written+1))
	u.written += n
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if u.written > wellKnownArtifactLimit {
		return fmt.Errorf("unpacked size exceeds %d MiB", wellKnownArtifactLimit>>20)
	}
	return nil
}

func (u *unpacker) tarGz(data []byte) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			err = u.mkdir(header.Name)
		case tar.TypeReg:
			err = u.file(header.Name, os.FileMode(header.Mode), tr)
		case tar.TypeXGlobalHeader:
			// pax metadata, not a member
		default:
			err = fmt.Errorf("member %q is not a regular file or directory", header.Name)
		}
		if err != nil {
			return err
		}
	}
}

func (u *unpacker) zip(data []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		mode := f.Mode()
		switch {
		case mode.IsDir():
			err = u.mkdir(f.Name)
		case mode.IsRegular():
			var r io.ReadCloser
			if r, err = f.Open(); err == nil {
				err = u.file(f.Name, mode, r)
				_ = r.Close()
			}
		default:
			err = fmt.Errorf("member %q is not a regular file or directory", f.Name)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// writeFileAtomic replaces path with data, so a reader never sees half of it.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
