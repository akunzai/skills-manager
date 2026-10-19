package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestConfigLoadAndSave(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "skills.json")

	// 1. Loading non-existent config should return default
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Version != 1 {
		t.Errorf("expected version 1, got %d", cfg.Version)
	}

	// 2. Add remote skill and local skill
	AddRemoteSkillEntry(cfg, "owner/repo", "skill-a", "skills/skill-a", "github", "")
	AddLocalSymlinkEntry(cfg, "local-skill", "/tmp/local-skill", "a local test skill")
	AddLocalCommandEntry(cfg, "cmd-skill", "echo install", "which echo", "a cmd skill")

	if err := SaveConfig(cfg, configPath); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	// 3. Reload config and verify contents
	loaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig re-load failed: %v", err)
	}

	if len(loaded.Remote) != 1 {
		t.Fatalf("expected 1 remote repo, got %d", len(loaded.Remote))
	}
	if loaded.Remote["owner/repo"].Skills["skill-a"] != "skills/skill-a" {
		t.Errorf("unexpected skill-a subpath")
	}
	if len(loaded.Local) != 2 {
		t.Fatalf("expected 2 local skills, got %d", len(loaded.Local))
	}
	// 4. Test FindSkillSource: the kind tells a command Skill from a symlink
	for _, tc := range []struct {
		name, src string
		kind      SkillKind
	}{
		{"skill-a", "owner/repo", SkillRemote},
		{"local-skill", "local-skill", SkillSymlink},
		{"cmd-skill", "cmd-skill", SkillCommand},
	} {
		kind, src, found := FindSkillSource(loaded, tc.name)
		if !found || kind != tc.kind || src != tc.src {
			t.Errorf("FindSkillSource(%s) = (%v, %v, %v); want (%v, %v, true)", tc.name, kind, src, found, tc.kind, tc.src)
		}
	}

	// 5. Test RemoveSkillEntry
	if !RemoveSkillEntry(loaded, "skill-a") {
		t.Errorf("expected RemoveSkillEntry for skill-a to return true")
	}
	if len(loaded.Remote) != 0 {
		t.Errorf("expected empty remote map after removing only skill in repo")
	}
}

func TestGetConfiguredSkillNames(t *testing.T) {
	cfg := DefaultConfig()
	AddRemoteSkillEntry(cfg, "owner/repo1", "skill-b", "b", "github", "")
	AddRemoteSkillEntry(cfg, "owner/repo2", "skill-a", "a", "github", "")
	AddLocalSymlinkEntry(cfg, "skill-c", "/tmp/c", "")

	names := GetConfiguredSkillNames(cfg)
	expected := []string{"skill-a", "skill-b", "skill-c"}
	if len(names) != len(expected) {
		t.Fatalf("expected %d names, got %d", len(expected), len(names))
	}
	for i, name := range names {
		if name != expected[i] {
			t.Errorf("names[%d] = %q; want %q", i, name, expected[i])
		}
	}
}

func TestAddSkillEntryReplacesConflictingSource(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Settings.Availability["my-skill"] = AvailabilityOverride{Include: []string{"codex"}}

	// 1. Add local symlink skill
	AddLocalSymlinkEntry(cfg, "my-skill", "/path/to/my-skill", "local")
	if _, ok := cfg.Local["my-skill"]; !ok {
		t.Fatal("expected my-skill in local")
	}

	// 2. Add remote skill with same name -> should replace local
	AddRemoteSkillEntry(cfg, "owner/repo", "my-skill", "skills/my-skill", "github", "")
	if _, ok := cfg.Local["my-skill"]; ok {
		t.Fatal("expected local entry for my-skill to be removed")
	}
	if cfg.Remote["owner/repo"].Skills["my-skill"] != "skills/my-skill" {
		t.Fatal("expected remote entry for my-skill")
	}

	// 3. Add local command skill with same name -> should replace remote
	AddLocalCommandEntry(cfg, "my-skill", "echo hi", "", "command")
	if _, ok := cfg.Remote["owner/repo"]; ok {
		t.Fatal("expected empty remote repo to be removed")
	}
	if cfg.Local["my-skill"].Command != "echo hi" {
		t.Fatal("expected local command entry for my-skill")
	}
	if len(cfg.Settings.Availability["my-skill"].Include) != 1 {
		t.Fatal("replacing a skill source must preserve its availability")
	}
	if !RemoveSkillEntry(cfg, "my-skill") {
		t.Fatal("expected skill removal to succeed")
	}
	if _, ok := cfg.Settings.Availability["my-skill"]; ok {
		t.Fatal("removing a skill must remove its availability override")
	}
}

func TestNormalizeAvailabilityCanonicalizesAgents(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Settings.Availability["sample"] = AvailabilityOverride{
		Include: []string{"claude", "CLAUDE-CODE", "continue"},
		Exclude: []string{"roo-code", "roo"},
	}
	if err := NormalizeAvailability(cfg); err != nil {
		t.Fatal(err)
	}
	got := cfg.Settings.Availability["sample"]
	if !reflect.DeepEqual(got.Include, []string{"claude-code", "continue"}) {
		t.Fatalf("include = %#v", got.Include)
	}
	if !reflect.DeepEqual(got.Exclude, []string{"roo"}) {
		t.Fatalf("exclude = %#v", got.Exclude)
	}
}

func TestNormalizeAvailabilityRejectsConflicts(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Settings.Availability["sample"] = AvailabilityOverride{
		Include: []string{"claude"},
		Exclude: []string{"claude-code"},
	}
	if err := NormalizeAvailability(cfg); err == nil {
		t.Fatal("expected include/exclude conflict")
	}
}

func TestSaveConfigDropsRemovedSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skills.json")
	raw := `{
  "version": 1,
  "settings": {
    "defaultAgents": ["claude"],
    "excludeAgents": ["continue"],
    "agentExclusions": {"continue": ["sample"]}
  },
  "postHooks": [{"name": "x", "run": "true"}]
}
`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(cfg, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	saved := string(data)
	for _, key := range []string{"excludeAgents", "agentExclusions", "postHooks"} {
		if strings.Contains(saved, key) {
			t.Fatalf("saved config still contains %s:\n%s", key, saved)
		}
	}
}

// A signature policy is replaced, never changed in place, so a shallow copy
// of Config, such as Adopt's undo snapshot, keeps the policy it had.
func TestSignaturePolicyUpdatesLeaveACopiedConfigUnchanged(t *testing.T) {
	original := &Config{Remote: map[string]RemoteRepo{"owner/repo": {
		Signature: &SignaturePolicy{Require: true, Trusted: map[string]string{"kept": "tree-1", "revoked": "tree-2"}},
	}}}
	want := SignaturePolicy{Require: true, Trusted: map[string]string{"kept": "tree-1", "revoked": "tree-2"}}
	shallow := *original
	shallow.Remote = map[string]RemoteRepo{"owner/repo": original.Remote["owner/repo"]}

	TrustContent(&shallow, "owner/repo", "added", "tree-3")
	RevokeTrust(&shallow, "revoked")
	PinSigstoreSigner(&shallow, "owner/repo", "https://example.com/signer", "https://issuer.example.com")
	SetCertificateChainTrust(&shallow, "owner/repo", "root.pem")

	if got := *original.Remote["owner/repo"].Signature; !reflect.DeepEqual(got, want) {
		t.Fatalf("original policy = %+v, want %+v", got, want)
	}
	got := *shallow.Remote["owner/repo"].Signature
	wantCopy := SignaturePolicy{Require: true, CertificateChain: "root.pem", Trusted: map[string]string{"kept": "tree-1", "added": "tree-3"}}
	if !reflect.DeepEqual(got, wantCopy) {
		t.Fatalf("copied policy = %+v, want %+v", got, wantCopy)
	}
}

func TestPinSigstoreSignerOnASourceWithNoPolicyYet(t *testing.T) {
	cfg := &Config{Remote: map[string]RemoteRepo{"owner/repo": {}}}

	PinSigstoreSigner(cfg, "owner/repo", "https://example.com/signer", "https://issuer.example.com")

	want := &SigstoreSigner{Identity: "https://example.com/signer", Issuer: "https://issuer.example.com"}
	if got := cfg.Remote["owner/repo"].Signature; got == nil || !reflect.DeepEqual(got.Sigstore, want) {
		t.Fatalf("policy = %+v, want Sigstore %+v", got, want)
	}
}

func TestSignaturePolicyUpdatesIgnoreAnUndeclaredSource(t *testing.T) {
	cfg := &Config{Remote: map[string]RemoteRepo{}}

	TrustContent(cfg, "owner/repo", "sample", "tree-1")
	PinSigstoreSigner(cfg, "owner/repo", "https://example.com/signer", "https://issuer.example.com")
	SetCertificateChainTrust(cfg, "owner/repo", "root.pem")

	if len(cfg.Remote) != 0 {
		t.Fatalf("Remote = %+v, want no Source declared", cfg.Remote)
	}
}

// A name trusted under two Sources, as a Skill that moved Source leaves
// behind, is revoked under both.
func TestRevokeTrustRemovesTheNameFromEverySource(t *testing.T) {
	cfg := &Config{Remote: map[string]RemoteRepo{
		"old/repo": {Signature: &SignaturePolicy{Trusted: map[string]string{"sample": "tree-1"}}},
		"new/repo": {Signature: &SignaturePolicy{Require: true, Trusted: map[string]string{"sample": "tree-2", "other": "tree-3"}}},
	}}

	if !RevokeTrust(cfg, "sample") {
		t.Fatal("RevokeTrust = false, want true")
	}

	if got := cfg.Remote["old/repo"].Signature; got != nil {
		t.Fatalf("old/repo policy = %+v, want none", got)
	}
	want := SignaturePolicy{Require: true, Trusted: map[string]string{"other": "tree-3"}}
	if got := *cfg.Remote["new/repo"].Signature; !reflect.DeepEqual(got, want) {
		t.Fatalf("new/repo policy = %+v, want %+v", got, want)
	}
	if RevokeTrust(cfg, "sample") {
		t.Fatal("second RevokeTrust = true, want false")
	}
}

func TestLoadConfigRefusesAWellKnownSourceWithABranchOrNoURL(t *testing.T) {
	for name, repo := range map[string]string{
		"branch": `{"type": "well-known", "url": "https://redocly.com", "branch": "main", "skills": {"a": "a"}}`,
		"no url": `{"type": "well-known", "skills": {"a": "a"}}`,
	} {
		path := filepath.Join(t.TempDir(), "skills.json")
		data := `{"version": 1, "remote": {"redocly.com": ` + repo + `}}`
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil {
			t.Errorf("%s: LoadConfig accepted %s", name, repo)
		}
	}
}
