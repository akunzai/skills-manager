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

var testSigner = signing.Signer{Identity: "https://github.com/owner/repo/.github/workflows/sign.yml@refs/heads/main", Issuer: "https://token.actions.githubusercontent.com"}

// stubVerify replaces signing.Verify for one test, recording the trust each
// call was given.
func stubVerify(t *testing.T, verify func(dir string, trust signing.Trust) (signing.Result, error)) *[]signing.Trust {
	t.Helper()
	var calls []signing.Trust
	previous := verifySkill
	verifySkill = func(dir string, trust signing.Trust) (signing.Result, error) {
		calls = append(calls, trust)
		return verify(dir, trust)
	}
	t.Cleanup(func() { verifySkill = previous })
	return &calls
}

func signedBy(signer signing.Signer) func(string, signing.Trust) (signing.Result, error) {
	return func(string, signing.Trust) (signing.Result, error) {
		return signing.Result{Signed: true, Signer: signer}, nil
	}
}

func unsigned(string, signing.Trust) (signing.Result, error) { return signing.Result{}, nil }

// signatureFixture declares one remote Skill, "sample", whose Cache is ready
// and whose Scope copy is missing, with Config saved at configPath.
func signatureFixture(t *testing.T) (cfg *config.Config, configPath, skillsDir, cacheDir string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	skillsDir, cacheDir = filepath.Join(root, "skills"), filepath.Join(root, "cache")
	origin := filepath.Join(root, "origin")
	writeLocalGitSkill(t, origin, "sample")
	if _, err := NewCache("owner/repo", origin, "", cacheDir).Refresh(false, "sample"); err != nil {
		t.Fatal(err)
	}
	cfg = config.DefaultConfig()
	config.AddRemoteSkillEntry(cfg, "owner/repo", "sample", "sample", "git", origin)
	configPath = filepath.Join(root, "skills.json")
	if err := config.SaveConfig(cfg, configPath); err != nil {
		t.Fatal(err)
	}
	return cfg, configPath, skillsDir, cacheDir
}

func syncOnce(t *testing.T, cfg *config.Config, configPath, skillsDir, cacheDir string, decision SyncDecision) (*SyncPlan, *SyncReport) {
	t.Helper()
	plan, err := PlanSync(cfg, configPath, skillsDir, cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	report, err := plan.Apply(decision, nil)
	if err != nil {
		t.Fatal(err)
	}
	return plan, report
}

func eventKinds(report *SyncReport) []string {
	var kinds []string
	for _, ev := range report.Events {
		kinds = append(kinds, ev.Kind)
	}
	return kinds
}

func TestSyncRecordsFirstKeylessSigner(t *testing.T) {
	cfg, configPath, skillsDir, cacheDir := signatureFixture(t)
	calls := stubVerify(t, signedBy(testSigner))

	_, report := syncOnce(t, cfg, configPath, skillsDir, cacheDir, SyncDecision{})

	if report.Blocked+report.Failed != 0 {
		t.Fatalf("report = %+v, want every Skill applied", report.SyncTally)
	}
	if (*calls)[0].Sigstore != nil {
		t.Fatalf("first verification pinned %+v, want any signer accepted", (*calls)[0].Sigstore)
	}
	if !slices.Contains(eventKinds(report), SyncSignerRecorded) {
		t.Fatalf("events = %v, want %s", eventKinds(report), SyncSignerRecorded)
	}
	saved, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := saved.Remote["owner/repo"].Signature
	if got == nil || got.Sigstore == nil || got.Sigstore.Identity != testSigner.Identity || got.Sigstore.Issuer != testSigner.Issuer {
		t.Fatalf("saved signature = %+v, want %+v", got, testSigner)
	}
	if applied, _ := OpenBaselines(skillsDir).Applied("sample"); !applied.Signed {
		t.Fatal("Baseline should record that the applied copy was signed")
	}

	// The recorded signer is what the next Sync verifies against.
	syncOnce(t, saved, configPath, skillsDir, cacheDir, SyncDecision{})
	if pinned := (*calls)[len(*calls)-1].Sigstore; pinned == nil || *pinned != testSigner {
		t.Fatalf("second verification pinned %+v, want %+v", pinned, testSigner)
	}
}

func TestSyncKeepsPreviousCopyWhenSignatureFails(t *testing.T) {
	cfg, configPath, skillsDir, cacheDir := signatureFixture(t)
	stubVerify(t, unsigned)
	syncOnce(t, cfg, configPath, skillsDir, cacheDir, SyncDecision{})
	scopeCopy := filepath.Join(skillsDir, "sample", "SKILL.md")
	before, err := os.ReadFile(scopeCopy)
	if err != nil {
		t.Fatal(err)
	}

	stubVerify(t, func(string, signing.Trust) (signing.Result, error) {
		return signing.Result{}, errors.New("SKILL.md changed after signing")
	})
	// --force overwrites local edits, never an unverified Source.
	plan, report := syncOnce(t, cfg, configPath, skillsDir, cacheDir, SyncDecision{Force: true})

	item := plan.Items[0]
	if item.Block != SyncBlockSignature {
		t.Fatalf("block = %q, want %q", item.Block, SyncBlockSignature)
	}
	if report.Blocked != 1 || report.Failed != 0 {
		t.Fatalf("report = %+v, want one blocked Skill (exit 1, ADR-0002)", report.SyncTally)
	}
	if plan.Forceable(SyncDecision{}) {
		t.Fatal("a signature block must not read as liftable by --force")
	}
	after, err := os.ReadFile(scopeCopy)
	if err != nil || string(after) != string(before) {
		t.Fatalf("the previous copy should stay in place: %q, %v", after, err)
	}
}

func TestSyncBlocksUnsignedSkillWhenRequired(t *testing.T) {
	cfg, configPath, skillsDir, cacheDir := signatureFixture(t)
	repo := cfg.Remote["owner/repo"]
	repo.Signature = &config.SignaturePolicy{Require: true}
	cfg.Remote["owner/repo"] = repo
	stubVerify(t, unsigned)

	plan, report := syncOnce(t, cfg, configPath, skillsDir, cacheDir, SyncDecision{})

	if plan.Items[0].Block != SyncBlockSignature || report.Blocked != 1 {
		t.Fatalf("item = %+v, report = %+v; want an unsigned Skill blocked", plan.Items[0], report.SyncTally)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "sample")); !os.IsNotExist(err) {
		t.Fatalf("a blocked Skill should not be Materialized: %v", err)
	}
}

func TestSyncBlocksSignedSkillArrivingUnsigned(t *testing.T) {
	cfg, configPath, skillsDir, cacheDir := signatureFixture(t)
	stubVerify(t, signedBy(testSigner))
	syncOnce(t, cfg, configPath, skillsDir, cacheDir, SyncDecision{})

	saved, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	stubVerify(t, unsigned)
	plan, report := syncOnce(t, saved, configPath, skillsDir, cacheDir, SyncDecision{})

	if plan.Items[0].Block != SyncBlockSignature || report.Blocked != 1 {
		t.Fatalf("item = %+v; want a downgrade to unsigned blocked", plan.Items[0])
	}
	if applied, _ := OpenBaselines(skillsDir).Applied("sample"); !applied.Signed {
		t.Fatal("a blocked downgrade must not clear the Baseline's signed mark")
	}
}

func TestSyncNamesUpdateWhenTrustRootIsMissing(t *testing.T) {
	cfg, configPath, skillsDir, cacheDir := signatureFixture(t)
	stubVerify(t, func(string, signing.Trust) (signing.Result, error) {
		return signing.Result{}, signing.ErrNoTrustRoot
	})

	plan, _ := syncOnce(t, cfg, configPath, skillsDir, cacheDir, SyncDecision{})

	if item := plan.Items[0]; item.Block != SyncBlockSignature || item.BlockNext != "update" {
		t.Fatalf("item = %+v, want a signature block naming update", item)
	}
}

func TestSyncLeavesUnsignedSkillsAlone(t *testing.T) {
	cfg, configPath, skillsDir, cacheDir := signatureFixture(t)
	stubVerify(t, unsigned)

	_, report := syncOnce(t, cfg, configPath, skillsDir, cacheDir, SyncDecision{})

	if report.Blocked+report.Failed != 0 {
		t.Fatalf("report = %+v, want an unsigned Skill applied", report.SyncTally)
	}
	saved, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Remote["owner/repo"].Signature != nil {
		t.Fatal("an unsigned Source should not gain a signature policy")
	}
	if applied, _ := OpenBaselines(skillsDir).Applied("sample"); applied.Signed {
		t.Fatal("Baseline should record an unsigned copy as unsigned")
	}
}

func TestSignatureTrustResolvesCertificateChainAgainstConfigDir(t *testing.T) {
	dir := t.TempDir()
	pem, err := os.ReadFile(filepath.Join("..", "signing", "testdata", "nvidia", "nv-agent-root-cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "root.pem"), pem, 0o644); err != nil {
		t.Fatal(err)
	}
	s := newSignatures(config.DefaultConfig(), filepath.Join(dir, "skills.json"), "", &Baselines{})
	trust, err := s.trust("src", &config.SignaturePolicy{CertificateChain: "root.pem"})
	if err != nil || trust.Roots == nil {
		t.Fatalf("trust = %+v, %v; want roots read from beside skills.json", trust, err)
	}
}

// addSignedFixture prepares a Remote intake of "sample" whose Skill carries a
// signature file, so Add has something to verify and a trust root to fetch.
func addSignedFixture(t *testing.T) (cfg *config.Config, configPath, skillsDir, cacheDir string, intake *RemoteIntake) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	skillsDir = filepath.Join(project, ".agents", "skills")
	cacheDir = filepath.Join(project, "cache")
	configPath = filepath.Join(project, ".agents", "skills.json")
	origin := filepath.Join(project, "origin")
	if err := os.MkdirAll(filepath.Join(origin, "sample"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, "sample", signing.FileName), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeLocalGitSkill(t, origin, "sample")
	cfg = config.DefaultConfig()
	intake = mustPrepareRemoteIntake(t, cfg, remoteSpec(origin, "", ""), cacheDir)
	return cfg, configPath, skillsDir, cacheDir, intake
}

func stubTrustedRoot(t *testing.T) *int {
	t.Helper()
	fetches := 0
	previous := fetchTrustedRoot
	fetchTrustedRoot = func(string) ([]byte, error) {
		fetches++
		return []byte(`{"mediaType":"application/vnd.dev.sigstore.trustedroot+json;version=0.1"}`), nil
	}
	t.Cleanup(func() { fetchTrustedRoot = previous })
	return &fetches
}

func TestAddRecordsSignerWithTheSkills(t *testing.T) {
	cfg, configPath, skillsDir, cacheDir, intake := addSignedFixture(t)
	fetches := stubTrustedRoot(t)
	stubVerify(t, signedBy(testSigner))

	plan := BuildAddPlan(cfg, configPath, skillsDir, NewRemoteAddSource(intake), map[string]string{"sample": "sample"}, AddAvailabilityIntent{})
	result, err := ApplyAddPlan(plan, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	if result.Blocked+result.Failed != 0 {
		t.Fatalf("result = %+v, want the Skill applied", result.SyncTally)
	}
	if *fetches != 1 {
		t.Fatalf("trust root fetched %d times, want once for a signed Skill", *fetches)
	}
	if _, err := os.Stat(trustRootPath(cacheDir)); err != nil {
		t.Fatalf("trust root should be cached for offline Sync: %v", err)
	}
	saved, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, repo := range saved.Remote {
		if repo.Signature == nil || repo.Signature.Sigstore == nil || repo.Signature.Sigstore.Identity != testSigner.Identity {
			t.Fatalf("saved signature = %+v, want %+v", repo.Signature, testSigner)
		}
	}
	if applied, _ := OpenBaselines(skillsDir).Applied("sample"); !applied.Signed {
		t.Fatal("Baseline should record the added copy as signed")
	}
}

func TestAddWithTrustCertVerifiesAgainstIt(t *testing.T) {
	cfg, configPath, skillsDir, _, intake := addSignedFixture(t)
	stubTrustedRoot(t)
	pem, err := os.ReadFile(filepath.Join("..", "signing", "testdata", "nvidia", "nv-agent-root-cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(configPath), "root.pem"), pem, 0o644); err != nil {
		t.Fatal(err)
	}
	calls := stubVerify(t, func(string, signing.Trust) (signing.Result, error) {
		return signing.Result{}, errors.New("certificate does not chain to the trusted certificate")
	})

	plan := BuildAddPlan(cfg, configPath, skillsDir, NewRemoteAddSource(intake), map[string]string{"sample": "sample"}, AddAvailabilityIntent{})
	plan.TrustCert = "root.pem"
	result, err := ApplyAddPlan(plan, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(*calls) == 0 || (*calls)[0].Roots == nil {
		t.Fatalf("verification should use the trusted certificate, got %+v", *calls)
	}
	if result.Blocked != 1 {
		t.Fatalf("result = %+v, want the unverified Skill blocked", result.SyncTally)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "sample")); !os.IsNotExist(err) {
		t.Fatalf("an unverified Skill should not be Materialized: %v", err)
	}
	saved, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, repo := range saved.Remote {
		if repo.Signature == nil || repo.Signature.CertificateChain != "root.pem" || repo.Signature.Sigstore != nil {
			t.Fatalf("saved signature = %+v, want certificateChain root.pem", repo.Signature)
		}
	}
}

func TestAddOfUnsignedSourceNeverFetchesTrustRoot(t *testing.T) {
	fetches := stubTrustedRoot(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := t.TempDir()
	origin := filepath.Join(project, "origin")
	writeLocalGitSkill(t, origin, "sample")
	cfg := config.DefaultConfig()
	intake := mustPrepareRemoteIntake(t, cfg, remoteSpec(origin, "", ""), filepath.Join(project, "cache"))
	plan := BuildAddPlan(cfg, filepath.Join(project, "skills.json"), filepath.Join(project, "skills"), NewRemoteAddSource(intake), map[string]string{"sample": "sample"}, AddAvailabilityIntent{})
	if _, err := ApplyAddPlan(plan, cfg, nil); err != nil {
		t.Fatal(err)
	}
	if *fetches != 0 {
		t.Fatalf("trust root fetched %d times for an unsigned Source", *fetches)
	}
}
