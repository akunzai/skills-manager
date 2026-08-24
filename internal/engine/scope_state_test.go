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

	got, err := DigestSkillContent(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"SKILL.md":        scopeStateTestSHA256("hello"),
		"nested/data.txt": scopeStateTestSHA256("world"),
		"link":            scopeStateTestSHA256("nested/data.txt"),
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

	store, err := NewScopeStateStore(filepath.Join(aliasDir, "skills.json"))
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
	store, err := NewScopeStateStore(filepath.Join(t.TempDir(), "skills.json"))
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
	realStore, err := NewScopeStateStore(filepath.Join(realDir, "skills.json"))
	if err != nil {
		t.Fatal(err)
	}
	aliasStore, err := NewScopeStateStore(filepath.Join(aliasDir, "skills.json"))
	if err != nil {
		t.Fatal(err)
	}
	if aliasStore.Path() != realStore.Path() {
		t.Fatalf("alias Path() = %q, want canonical %q", aliasStore.Path(), realStore.Path())
	}
}

func TestScopeStateStoreCorruptLoadReturnsErrorAndPreservesArtifact(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := NewScopeStateStore(filepath.Join(t.TempDir(), "skills.json"))
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

func TestScopeStateStoreDeleteSkillAndPrune(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := NewScopeStateStore(filepath.Join(t.TempDir(), "skills.json"))
	if err != nil {
		t.Fatal(err)
	}
	initial := ScopeState{Skills: map[string]AppliedSkillState{
		"keep":   {Source: "source"},
		"remove": {Source: "source"},
		"stale":  {Source: "source"},
	}}
	if err := store.Save(initial); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSkill("remove"); err != nil {
		t.Fatal(err)
	}
	if err := store.PruneSkills(map[string]struct{}{"keep": {}}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Skills["keep"]; !ok || len(got.Skills) != 1 {
		t.Fatalf("Skills = %#v, want only keep", got.Skills)
	}
	if err := store.Prune(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.Path()); !os.IsNotExist(err) {
		t.Fatalf("state artifact still exists: %v", err)
	}
}

func TestScopeStateStoreCleanupDoesNotCreateMissingArtifact(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := NewScopeStateStore(filepath.Join(t.TempDir(), "skills.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSkill("absent"); err != nil {
		t.Fatal(err)
	}
	if err := store.PruneSkills(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.Path()); !os.IsNotExist(err) {
		t.Fatalf("cleanup created missing state artifact: %v", err)
	}
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
