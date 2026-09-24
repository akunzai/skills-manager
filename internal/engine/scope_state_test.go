package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDigestSkillContentHashesFilesAndSymlinkTargets(t *testing.T) {
	root := t.TempDir()
	mustWriteScopeStateTestFile(t, filepath.Join(root, "SKILL.md"), []byte("hello"))
	mustWriteScopeStateTestFile(t, filepath.Join(root, "nested", "data.txt"), []byte("world"))
	if err := os.Symlink("nested/data.txt", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	// A multi-segment target written with the platform's own separator is
	// where the two diverge: Readlink hands it back separator for separator.
	// The digests below are the forward-slash form on every platform, so a
	// Scope state file created on one machine stays meaningful on another.
	if err := os.Symlink(filepath.Join("..", "SKILL.md"), filepath.Join(root, "nested", "up")); err != nil {
		t.Fatal(err)
	}

	got, err := DigestSkillContent(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"SKILL.md":        scopeStateTestSHA256("hello"),
		"nested/data.txt": scopeStateTestSHA256("world"),
		"link":            scopeStateTestSHA256("nested/data.txt"),
		"nested/up":       scopeStateTestSHA256("../SKILL.md"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("digest map = %#v, want %#v", got, want)
	}
}

func TestDigestSkillContentExcludesMaterializationNoise(t *testing.T) {
	root := t.TempDir()
	mustWriteScopeStateTestFile(t, filepath.Join(root, "keep.py"), []byte("keep"))
	mustWriteScopeStateTestFile(t, filepath.Join(root, ".git", "config"), []byte("ignored"))
	mustWriteScopeStateTestFile(t, filepath.Join(root, "pkg", "__pycache__", "cache"), []byte("ignored"))
	mustWriteScopeStateTestFile(t, filepath.Join(root, "pkg", "compiled.pyc"), []byte("ignored"))

	got, err := DigestSkillContent(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"keep.py": scopeStateTestSHA256("keep")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("digest map = %#v, want %#v", got, want)
	}
}

func TestScopeStateStoreRoundTripsVersionedStateAtCanonicalScopeKey(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	parent := t.TempDir()
	realScope := filepath.Join(parent, "real", "skills.json")
	mustWriteScopeStateTestFile(t, realScope, []byte("{}"))
	aliasDir := filepath.Join(parent, "alias")
	if err := os.Symlink(filepath.Join(parent, "real"), aliasDir); err != nil {
		t.Fatal(err)
	}

	store, err := newScopeStateStore(filepath.Join(aliasDir, "skills.json"))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(realScope)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		t.Fatal(err)
	}
	wantKey := sha256.Sum256([]byte(filepath.Clean(canonical)))
	wantPath := filepath.Join(stateHome, "skills-manager", "scope-state", hex.EncodeToString(wantKey[:])+".json")
	if store.Path() != wantPath {
		t.Fatalf("Path() = %q, want %q", store.Path(), wantPath)
	}

	want := ScopeState{
		Skills: map[string]AppliedSkillState{
			"alpha": {
				Source:         "owner/repo",
				CacheIdentity:  "https://example.test/repo.git@main",
				AppliedCommit:  "0123456789abcdef",
				ContentDigests: map[string]string{"SKILL.md": scopeStateTestSHA256("hello")},
			},
		},
	}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != ScopeStateVersion {
		t.Fatalf("Version = %d, want %d", got.Version, ScopeStateVersion)
	}
	if got.ScopePath != canonical {
		t.Fatalf("ScopePath = %q, want %q", got.ScopePath, canonical)
	}
	want.Version = ScopeStateVersion
	want.ScopePath = canonical
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
}

func TestScopeStateStoreMissingStateLoadsEmpty(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := newScopeStateStore(filepath.Join(t.TempDir(), "skills.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != ScopeStateVersion || len(got.Skills) != 0 {
		t.Fatalf("Load() = %#v, want empty versioned state", got)
	}
}

func TestScopeStateStoreCanonicalizesExistingParentOfMissingScopePath(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	parent := t.TempDir()
	realDir := filepath.Join(parent, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasDir := filepath.Join(parent, "alias")
	if err := os.Symlink(realDir, aliasDir); err != nil {
		t.Fatal(err)
	}
	realStore, err := newScopeStateStore(filepath.Join(realDir, "skills.json"))
	if err != nil {
		t.Fatal(err)
	}
	aliasStore, err := newScopeStateStore(filepath.Join(aliasDir, "skills.json"))
	if err != nil {
		t.Fatal(err)
	}
	if aliasStore.Path() != realStore.Path() {
		t.Fatalf("alias Path() = %q, want canonical %q", aliasStore.Path(), realStore.Path())
	}
}

func TestScopeStateStoreCorruptLoadReturnsErrorAndPreservesArtifact(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := newScopeStateStore(filepath.Join(t.TempDir(), "skills.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := []byte("{not json")
	if err := os.WriteFile(store.Path(), bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("Load() error = nil, want corrupt state error")
	}
	got, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, bad) {
		t.Fatalf("artifact changed to %q, want %q", got, bad)
	}
}

// writeUnreadableScopeState plants a Scope state for skillsDir that does not
// decode, returning its path and bytes.
func writeUnreadableScopeState(t *testing.T, skillsDir string) (string, []byte) {
	t.Helper()
	store, err := newScopeStateStore(skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	bad := []byte("{not json")
	mustWriteScopeStateTestFile(t, store.Path(), bad)
	return store.Path(), bad
}

func mustWriteScopeStateTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func scopeStateTestSHA256(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
