package engine

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/signing"
)

func tampered(string, signing.Trust) (signing.Result, error) {
	return signing.Result{}, errors.New("SKILL.md changed after signing")
}

func trustOnce(t *testing.T, cfg *config.Config, configPath, skillsDir, cacheDir string, names ...string) []TrustItem {
	t.Helper()
	items, err := PlanTrust(cfg, configPath, skillsDir, cacheDir, names)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyTrust(cfg, configPath, items); err != nil {
		t.Fatal(err)
	}
	return items
}

func TestSyncMaterializesTrustedContent(t *testing.T) {
	cfg, configPath, skillsDir, cacheDir := signatureFixture(t)
	stubVerify(t, tampered)

	items := trustOnce(t, cfg, configPath, skillsDir, cacheDir, "sample")

	if len(items) != 1 || items[0].Refused != "" || items[0].Tree == "" || items[0].Reason != "SKILL.md changed after signing" {
		t.Fatalf("trust = %+v, want sample trusted at its tree", items)
	}
	saved, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.Remote["owner/repo"].Signature.Trusted["sample"]; got != items[0].Tree {
		t.Fatalf("saved Trusted content = %q, want %q", got, items[0].Tree)
	}

	_, report := syncOnce(t, saved, configPath, skillsDir, cacheDir, SyncDecision{})

	if report.Blocked+report.Failed != 0 {
		t.Fatalf("report = %+v, want trusted content applied (exit 0)", report.SyncTally)
	}
	if !slices.Contains(eventKinds(report), SyncTrustedUnverified) {
		t.Fatalf("events = %v, want %s", eventKinds(report), SyncTrustedUnverified)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "sample", "SKILL.md")); err != nil {
		t.Fatalf("trusted content should be Materialized: %v", err)
	}
	applied, _ := OpenBaselines(skillsDir).Applied("sample")
	if applied.Signed || applied.Unverified != "SKILL.md changed after signing" {
		t.Fatalf("Baseline = %+v, want it recorded as trusted unverified", applied)
	}
}

func TestSyncBlocksTrustedContentOnceItChanges(t *testing.T) {
	cfg, configPath, skillsDir, cacheDir := signatureFixture(t)
	stubVerify(t, tampered)
	trustOnce(t, cfg, configPath, skillsDir, cacheDir, "sample")
	origin := cfg.Remote["owner/repo"].URL
	if err := os.WriteFile(filepath.Join(origin, "sample", "SKILL.md"), []byte("# Changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, origin, "commit", "-am", "change")
	if _, err := NewCache("owner/repo", origin, "", cacheDir).Refresh(true, "sample"); err != nil {
		t.Fatal(err)
	}

	plan, report := syncOnce(t, cfg, configPath, skillsDir, cacheDir, SyncDecision{Force: true})

	if item := plan.Items[0]; item.Block != SyncBlockSignature || item.BlockNext != "trust sample" {
		t.Fatalf("item = %+v, want changed content blocked naming trust", item)
	}
	if report.Blocked != 1 {
		t.Fatalf("report = %+v, want one blocked Skill", report.SyncTally)
	}
	saved, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Remote["owner/repo"].Signature.Trusted["sample"] == "" {
		t.Fatal("Sync must not remove Trusted content (ADR-0007)")
	}
}

func TestPlanTrustRefusesWhatIsNotBlockedBySignature(t *testing.T) {
	cfg, configPath, skillsDir, cacheDir := signatureFixture(t)
	stubVerify(t, unsigned)

	items, err := PlanTrust(cfg, configPath, skillsDir, cacheDir, []string{"sample", "missing"})
	if err != nil {
		t.Fatal(err)
	}

	if len(items) != 2 || items[0].Refused == "" || items[1].Refused == "" {
		t.Fatalf("trust = %+v, want both refused", items)
	}
}

func TestPlanTrustRefusesAMissingTrustRoot(t *testing.T) {
	cfg, configPath, skillsDir, cacheDir := signatureFixture(t)
	stubVerify(t, func(string, signing.Trust) (signing.Result, error) {
		return signing.Result{}, signing.ErrNoTrustRoot
	})

	items, err := PlanTrust(cfg, configPath, skillsDir, cacheDir, []string{"sample"})
	if err != nil {
		t.Fatal(err)
	}

	if items[0].Refused == "" || items[0].Next != "update" {
		t.Fatalf("trust = %+v, want a missing trust root refused naming update", items)
	}
}

func TestRevokeTrustRemovesTrustedContent(t *testing.T) {
	cfg, configPath, skillsDir, cacheDir := signatureFixture(t)
	stubVerify(t, tampered)
	trustOnce(t, cfg, configPath, skillsDir, cacheDir, "sample")

	revoked, err := RevokeTrust(cfg, configPath, []string{"sample", "other"})
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(revoked, []string{"sample"}) {
		t.Fatalf("revoked = %v, want [sample]", revoked)
	}
	saved, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.Remote["owner/repo"].Signature.TrustedTree("sample"); ok {
		t.Fatal("Trusted content should be gone from Config")
	}
	if plan, _ := syncOnce(t, saved, configPath, skillsDir, cacheDir, SyncDecision{}); plan.Items[0].Block != SyncBlockSignature {
		t.Fatal("a revoked Skill should be blocked again")
	}
}

func TestRetireRemovesTrustedContent(t *testing.T) {
	cfg, configPath, skillsDir, cacheDir := signatureFixture(t)
	stubVerify(t, tampered)
	trustOnce(t, cfg, configPath, skillsDir, cacheDir, "sample")
	config.AddRemoteSkillEntry(cfg, "owner/repo", "other", "sample", "git", cfg.Remote["owner/repo"].URL)

	if _, err := Retire(cfg, configPath, skillsDir, []string{"sample"}, OpenBaselines(skillsDir)); err != nil {
		t.Fatal(err)
	}

	saved, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.Remote["owner/repo"].Signature.TrustedTree("sample"); ok {
		t.Fatal("Retire should remove the Skill's Trusted content")
	}
}

func TestDoctorWarnsAboutTrustedContent(t *testing.T) {
	cfg, configPath, skillsDir, cacheDir := signatureFixture(t)
	stubVerify(t, tampered)
	trustOnce(t, cfg, configPath, skillsDir, cacheDir, "sample")
	syncOnce(t, cfg, configPath, skillsDir, cacheDir, SyncDecision{})

	outcome, err := NewDoctorWithCache(cfg, skillsDir, cacheDir).Run(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := outcome.Report.Unverified; len(got) != 1 || got[0].Name != "sample" || got[0].Reason != "SKILL.md changed after signing" {
		t.Fatalf("unverified = %+v, want sample", got)
	}
	if len(outcome.Report.StaleTrust) != 0 {
		t.Fatalf("stale = %v, want none while the content matches", outcome.Report.StaleTrust)
	}
	if !slices.ContainsFunc(outcome.Warnings, func(w DoctorWarning) bool { return w.Kind == DoctorFindingUnverified }) {
		t.Fatalf("warnings = %+v, want an unverified warning", outcome.Warnings)
	}

	origin := cfg.Remote["owner/repo"].URL
	if err := os.WriteFile(filepath.Join(origin, "sample", "SKILL.md"), []byte("# Changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, origin, "commit", "-am", "change")
	if _, err := NewCache("owner/repo", origin, "", cacheDir).Refresh(true, "sample"); err != nil {
		t.Fatal(err)
	}
	outcome, err = NewDoctorWithCache(cfg, skillsDir, cacheDir).Run(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(outcome.Report.StaleTrust, []string{"sample"}) {
		t.Fatalf("stale = %v, want [sample] once the content changed", outcome.Report.StaleTrust)
	}
}
