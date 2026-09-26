package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestEnsureGitRepoConfiguresLongpathsOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("skipping Windows-specific core.longpaths test on non-windows platform")
	}

	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")

	cacheDir := filepath.Join(root, "cache")
	repoDir, err := NewCache("owner/repo", origin, "", cacheDir).Refresh(false)
	if err != nil {
		t.Fatalf("EnsureGitRepo failed: %v", err)
	}

	val, _, err := runGit(repoDir, "config", "--local", "--get", "core.longpaths")
	if err != nil {
		t.Fatalf("git config core.longpaths failed: %v", err)
	}
	if val != "true" {
		t.Fatalf("expected core.longpaths to be 'true', got %q", val)
	}

	// Test update path on existing repo
	// Remove the config key first to simulate an older cache repo
	if _, _, err := runGit(repoDir, "config", "--local", "--unset", "core.longpaths"); err != nil {
		t.Fatalf("failed to unset core.longpaths: %v", err)
	}

	// Calling EnsureGitRepo again should re-apply core.longpaths on Windows
	if _, err := NewCache("owner/repo", origin, "", cacheDir).Refresh(false); err != nil {
		t.Fatalf("EnsureGitRepo update failed: %v", err)
	}

	valAfter, _, err := runGit(repoDir, "config", "--local", "--get", "core.longpaths")
	if err != nil {
		t.Fatalf("git config core.longpaths failed after update: %v", err)
	}
	if valAfter != "true" {
		t.Fatalf("expected core.longpaths to be restored to 'true', got %q", valAfter)
	}
}

func TestParseGitVersion(t *testing.T) {
	for _, tc := range []struct {
		output       string
		major, minor int
		ok           bool
	}{
		{"git version 2.55.0", 2, 55, true},
		{"git version 2.45.1.windows.1", 2, 45, true},
		{"git version 2.39.5 (Apple Git-154)", 2, 39, true},
		{"git version 3.0", 3, 0, true},
		{"usage: git", 0, 0, false},
		{"git version two", 0, 0, false},
	} {
		major, minor, ok := parseGitVersion(tc.output)
		if major != tc.major || minor != tc.minor || ok != tc.ok {
			t.Errorf("parseGitVersion(%q) = %d, %d, %v; want %d, %d, %v", tc.output, major, minor, ok, tc.major, tc.minor, tc.ok)
		}
	}
}

// Paths past Windows' MAX_PATH once under a temporary directory. The sparse
// Cache must never check them out unless a Scope declares them. A name
// Windows cannot write at all (aux, con, a ':') is no use here: Git for
// Windows rejects it when reading the tree into the index, sparse or not.
var (
	longFixture = "fixtures/" + strings.Repeat("deep/", 50) + "fixture.txt"
	longInSkill = "skills/beta/" + strings.Repeat("nested/", 40) + "fixture.txt"
)

// writeSparseOrigin commits two Skills, an identical mirror of the first, and
// paths past MAX_PATH, then returns a file:// URL. A plain path would
// make git ignore --filter and --depth, so the Cache would not be a partial
// clone; the plain path is returned too. https://git-scm.com/docs/git-clone#Documentation/git-clone.txt---no-local
func writeSparseOrigin(t *testing.T) (string, string) {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin")
	for path, content := range map[string]string{
		"README.md":                  "# Origin\n",
		"skills/alpha/SKILL.md":      "---\nname: alpha\n---\n",
		"skills/alpha/notes.txt":     "alpha\n",
		"mirror/alpha/SKILL.md":      "---\nname: alpha\n---\n",
		"mirror/alpha/notes.txt":     "alpha\n",
		"skills/beta/SKILL.md":       "---\nname: beta\n---\n",
		"skills/beta/reference.txt":  "beta\n",
		"fixtures/large/fixture.txt": "large\n",
	} {
		file := filepath.Join(origin, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustGit(t, origin, "init")
	mustGit(t, origin, "config", "user.email", "test@example.com")
	mustGit(t, origin, "config", "user.name", "test")
	mustGit(t, origin, "config", "uploadpack.allowFilter", "true")
	mustGit(t, origin, "add", ".")
	// Stage the long paths without touching the filesystem, so the fixture
	// builds on Windows too.
	blob := mustGit(t, origin, "hash-object", "-w", "README.md")
	for _, path := range []string{longFixture, longInSkill} {
		mustGit(t, origin, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+path)
	}
	mustGit(t, origin, "commit", "-m", "init")
	return origin, localFileURL(origin)
}

// refreshedCache is a Cache of a one-Skill local origin, with the commit it
// has checked out.
func refreshedCache(t *testing.T) (Cache, string) {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin")
	writeLocalGitSkill(t, origin, "sample")
	cache := NewCache("owner/repo", origin, "", t.TempDir())
	if _, err := cache.Refresh(false, "sample"); err != nil {
		t.Fatal(err)
	}
	return cache, mustGit(t, origin, "rev-parse", "HEAD")
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	stdout, stderr, err := runGit(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s\n%s", args, err, stdout, stderr)
	}
	return stdout
}

func assertCachePaths(t *testing.T, repoDir string, present, absent []string) {
	t.Helper()
	for _, path := range present {
		if _, err := os.Lstat(filepath.Join(repoDir, filepath.FromSlash(path))); err != nil {
			t.Errorf("%s should be checked out: %v", path, err)
		}
	}
	for _, path := range absent {
		if _, err := os.Lstat(filepath.Join(repoDir, filepath.FromSlash(path))); !os.IsNotExist(err) {
			t.Errorf("%s should not be checked out (err=%v)", path, err)
		}
	}
}

func TestEnsureGitRepoChecksOutOnlyDeclaredSkills(t *testing.T) {
	t.Parallel()
	_, url := writeSparseOrigin(t)
	repoDir, err := NewCache("owner/repo", url, "", t.TempDir()).Refresh(false, "skills/alpha")
	if err != nil {
		t.Fatal(err)
	}
	assertCachePaths(t, repoDir,
		[]string{"README.md", "skills/alpha/SKILL.md", "skills/alpha/notes.txt"},
		[]string{"skills/beta", "mirror", "fixtures", longFixture, longInSkill})
	if got := mustGit(t, repoDir, "config", "remote.origin.promisor"); got != "true" {
		t.Fatalf("Cache is not a partial clone: remote.origin.promisor = %q", got)
	}
	// The blob of a path no Scope declares was never downloaded.
	fixture := mustGit(t, repoDir, "rev-parse", "HEAD:fixtures/large/fixture.txt")
	missing := mustGit(t, repoDir, "rev-list", "--objects", "--missing=print", "HEAD")
	if !strings.Contains(missing, "?"+fixture) {
		t.Fatalf("undeclared blob %s was downloaded:\n%s", fixture, missing)
	}
}

// The Cache is shared by every Scope: a later Scope's paths join the sparse
// checkout without evicting an earlier Scope's (ADR 0004).
func TestEnsureGitRepoAddsSubpathsWithoutRemovingOthers(t *testing.T) {
	t.Parallel()
	_, url := writeSparseOrigin(t)
	cacheDir := t.TempDir()
	if _, err := NewCache("owner/repo", url, "", cacheDir).Refresh(false, "skills/alpha"); err != nil {
		t.Fatal(err)
	}
	repoDir, err := NewCache("owner/repo", url, "", cacheDir).Refresh(true, "skills/beta/")
	if err != nil {
		t.Fatal(err)
	}
	assertCachePaths(t, repoDir,
		[]string{"skills/alpha/notes.txt", "skills/beta/reference.txt"},
		[]string{"mirror", "fixtures"})
}

func TestEnsureGitRepoChecksOutWholeTreeForRootSkill(t *testing.T) {
	origin := filepath.Join(t.TempDir(), "origin")
	writeLocalGitSkill(t, origin, ".")
	cacheDir := t.TempDir()
	repoDir, err := NewCache("owner/repo", origin, "", cacheDir).Refresh(false, ".")
	if err != nil {
		t.Fatal(err)
	}
	assertCachePaths(t, repoDir, []string{"SKILL.md"}, nil)
	if coverage, err := NewCache("owner/repo", origin, "", cacheDir).coverage(); err != nil || len(coverage.missing([]string{"."})) != 0 {
		t.Fatalf("coverage = %+v, %v; want the root covered", coverage, err)
	}
}

// A Cache cloned in full by an older release is narrowed on the next refresh
// that fetches, before the new commit is written (ADR 0004).
func TestEnsureGitRepoNarrowsFullCacheOnRefresh(t *testing.T) {
	origin := filepath.Join(t.TempDir(), "origin")
	writeLocalGitSkill(t, origin, "alpha")
	writeLocalGitSkill(t, origin, "beta")
	branch := mustGit(t, origin, "symbolic-ref", "--short", "HEAD")
	cacheDir := t.TempDir()
	cache := resolveCacheRepo("owner/repo", origin, branch, cacheDir)
	if err := os.MkdirAll(filepath.Dir(cache.Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, "", "clone", origin, cache.Dir)

	repoDir, err := NewCache("owner/repo", origin, branch, cacheDir).Refresh(true, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	assertCachePaths(t, repoDir, []string{"alpha/SKILL.md"}, []string{"beta"})
}

func TestWithSkillFilesChecksOutOnlySkillMDThenRestoresCone(t *testing.T) {
	t.Parallel()
	_, url := writeSparseOrigin(t)
	repoDir, err := NewCache("owner/repo", url, "", t.TempDir()).Refresh(false, "skills/alpha")
	if err != nil {
		t.Fatal(err)
	}
	err = withSkillFiles(repoDir, func() error {
		assertCachePaths(t, repoDir,
			[]string{"skills/alpha/notes.txt", "skills/beta/SKILL.md", "mirror/alpha/SKILL.md"},
			[]string{"skills/beta/reference.txt", "mirror/alpha/notes.txt", "fixtures", longInSkill})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCachePaths(t, repoDir,
		[]string{"skills/alpha/notes.txt"},
		[]string{"skills/beta", "mirror", "fixtures"})
	if got := mustGit(t, repoDir, "sparse-checkout", "list"); got != "skills/alpha" {
		t.Fatalf("cone after discovery = %q", got)
	}
}

// An add interrupted during discovery leaves the Cache in non-cone mode; the
// next refresh restores the cone it had.
func TestEnsureGitRepoRestoresConeAfterInterruptedDiscovery(t *testing.T) {
	t.Parallel()
	_, url := writeSparseOrigin(t)
	cacheDir := t.TempDir()
	repoDir, err := NewCache("owner/repo", url, "", cacheDir).Refresh(false, "skills/alpha")
	if err != nil {
		t.Fatal(err)
	}
	state, err := readSparseState(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkOutSkillFiles(repoDir, state); err != nil {
		t.Fatal(err)
	}

	if _, err := NewCache("owner/repo", url, "", cacheDir).Refresh(false, "skills/beta"); err != nil {
		t.Fatal(err)
	}
	assertCachePaths(t, repoDir,
		[]string{"skills/alpha/notes.txt", "skills/beta/reference.txt"},
		[]string{"mirror", "fixtures"})
	if got := mustGit(t, repoDir, "config", "--bool", "core.sparseCheckoutCone"); got != "true" {
		t.Fatalf("core.sparseCheckoutCone = %q", got)
	}
}

func TestDirPatternRoundTripsGlobCharacters(t *testing.T) {
	for _, dir := range []string{"skills/alpha", "skills/[draft]", "a*b/c?d", `back\slash`, "!bang", "#hash"} {
		got, ok := unescapeDirPattern(escapeDirPattern(dir))
		if !ok || got != dir {
			t.Errorf("round trip of %q = %q, %v", dir, got, ok)
		}
	}
	for _, pattern := range []string{"/*", "!/*/", "[Ss][Kk][Ii][Ll][Ll].[Mm][Dd]", "/a*/"} {
		if dir, ok := unescapeDirPattern(pattern); ok {
			t.Errorf("unescapeDirPattern(%q) = %q; want rejected", pattern, dir)
		}
	}
}

// A root Skill's full checkout is deliberate, not an earlier release's full
// clone, so another Scope's fetching refresh leaves it whole (ADR 0004).
func TestEnsureGitRepoKeepsRootSkillCheckoutForOtherScopes(t *testing.T) {
	t.Parallel()
	_, url := writeSparseOrigin(t)
	cacheDir := t.TempDir()
	if _, err := NewCache("owner/repo", url, "", cacheDir).Refresh(false, "."); err != nil {
		t.Fatal(err)
	}
	repoDir, err := NewCache("owner/repo", url, "", cacheDir).Refresh(true, "skills/alpha")
	if err != nil {
		t.Fatal(err)
	}
	assertCachePaths(t, repoDir, []string{"skills/beta/reference.txt", "fixtures/large/fixture.txt"}, nil)
	if coverage, err := NewCache("owner/repo", url, "", cacheDir).coverage(); err != nil || len(coverage.missing([]string{"."})) != 0 {
		t.Fatalf("coverage = %+v, %v; want the root covered", coverage, err)
	}
}

// The Cache answers what its head commit holds from the trees a blobless
// clone always has, so paths outside the sparse checkout are answered too,
// and no blob is downloaded to answer.
func TestCacheAnswersHeadAndSubpathsOffline(t *testing.T) {
	t.Parallel()
	origin, url := writeSparseOrigin(t)
	cache := NewCache("owner/repo", url, "", t.TempDir())
	if head := cache.head(); head != "" {
		t.Fatalf("head of an absent Cache = %q; want none", head)
	}
	if entry := cache.atHead("skills/alpha"); entry != (headEntry{}) {
		t.Fatalf("atHead in an absent Cache = %+v; want none", entry)
	}
	if _, err := cache.Refresh(false, "skills/alpha"); err != nil {
		t.Fatal(err)
	}

	if got, want := cache.head(), mustGit(t, origin, "rev-parse", "HEAD"); got != want {
		t.Fatalf("head = %q; want %q", got, want)
	}
	for _, tc := range []struct {
		subpath, rev string
		dir          bool
	}{
		{subpath: "skills/alpha", rev: "HEAD:skills/alpha", dir: true},
		{subpath: "skills/beta/", rev: "HEAD:skills/beta", dir: true},
		{subpath: "skills/beta/reference.txt", rev: "HEAD:skills/beta/reference.txt"},
		{subpath: ".", rev: "HEAD^{tree}", dir: true},
	} {
		want := headEntry{id: mustGit(t, origin, "rev-parse", tc.rev), dir: tc.dir}
		if got := cache.atHead(tc.subpath); got != want {
			t.Errorf("atHead(%q) = %+v; want %+v", tc.subpath, got, want)
		}
	}
	for _, subpath := range []string{"skills/gamma", "skills/alpha/nested", "skills/al*"} {
		if got := cache.atHead(subpath); got.exists() {
			t.Errorf("atHead(%q) = %+v; want none", subpath, got)
		}
	}
	reference := mustGit(t, origin, "rev-parse", "HEAD:skills/beta/reference.txt")
	if missing := mustGit(t, cache.dir(), "rev-list", "--objects", "--missing=print", "HEAD"); !strings.Contains(missing, "?"+reference) {
		t.Fatalf("answering downloaded blob %s:\n%s", reference, missing)
	}
}

func TestCacheCoverageNamesMissingSubpaths(t *testing.T) {
	t.Parallel()
	_, url := writeSparseOrigin(t)
	cache := NewCache("owner/repo", url, "", t.TempDir())
	if _, err := cache.Refresh(false, "skills/alpha"); err != nil {
		t.Fatal(err)
	}
	coverage, err := cache.coverage()
	if err != nil {
		t.Fatal(err)
	}
	got := coverage.missing([]string{"skills/alpha", "skills/alpha/notes.txt", "skills/beta/", "."})
	if want := []string{".", "skills/beta"}; !slices.Equal(got, want) {
		t.Fatalf("missing = %v; want %v", got, want)
	}
	if !coverage.covers("skills/alpha") || coverage.covers("skills/beta") {
		t.Fatalf("covers alpha = %v, beta = %v; want true, false", coverage.covers("skills/alpha"), coverage.covers("skills/beta"))
	}
}
