package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/engine"
	"github.com/akunzai/skills-manager/internal/signing"
	"github.com/akunzai/skills-manager/internal/wellknowntest"
)

// The mise fixture is signed keyless by miseSigner; its trust root is served
// offline in place of Sigstore's. Fixture paths are absolute because the
// tests change directory into their project.
const miseSigner = "https://github.com/akunzai/agent-skills/.github/workflows/sign-skills.yml@refs/heads/main"

var (
	miseFixture, _    = filepath.Abs(filepath.Join("..", "signing", "testdata", "agent-skills"))
	nvidiaRootCert, _ = filepath.Abs(filepath.Join("..", "signing", "testdata", "nvidia", "nv-agent-root-cert.pem"))
)

// offlineTrustRoot serves the fixture's Sigstore trust root and counts how
// many times a command fetched it.
func offlineTrustRoot(t *testing.T) *int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(miseFixture, "trusted_root.json"))
	if err != nil {
		t.Fatal(err)
	}
	fetches := new(int)
	previous := engine.FetchTrustedRoot
	engine.FetchTrustedRoot = func(string) ([]byte, error) {
		*fetches++
		return data, nil
	}
	t.Cleanup(func() { engine.FetchTrustedRoot = previous })
	return fetches
}

// miseArchive is the signed mise Skill as an archive entry, with its SKILL.md
// replaced by skillMD when that is not "", and without its signature when
// signed is false.
func miseArchive(t *testing.T, skillMD string, signed bool) wellknowntest.Entry {
	t.Helper()
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(miseFixture, "mise", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if skillMD == "" {
		skillMD = read("SKILL.md")
	}
	files := []wellknowntest.File{{Name: "SKILL.md", Body: skillMD}}
	if signed {
		files = append(files, wellknowntest.File{Name: signing.FileName, Body: read(signing.FileName)})
	}
	return wellknowntest.TarGz(t, "mise", "mise", files...)
}

func wellKnownPolicy(t *testing.T, scope wellKnownScope, server *wellknowntest.Server) *config.SignaturePolicy {
	t.Helper()
	cfg, err := config.LoadConfig(scope.config)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Remote[strings.TrimPrefix(server.URL, "https://")].Signature
}

func scopeSkillMD(t *testing.T, scope wellKnownScope, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(scope.skills, name, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCLIAddVerifiesASignedWellKnownArchiveAndTrustLetsATamperedOneThrough(t *testing.T) {
	fetches := offlineTrustRoot(t)
	server := newWellKnownServer(t, miseArchive(t, "", true))
	scope := newWellKnownScope(t)
	original, err := os.ReadFile(filepath.Join(miseFixture, "mise", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}

	out, err := scope.run(t, "add", server.URL, "--skill", "mise", "-y")
	if err != nil || *fetches != 1 {
		t.Fatalf("add = %v, %d trust root fetches; want exit 0 after one fetch\n%s", err, *fetches, out)
	}
	policy := wellKnownPolicy(t, scope, server)
	if policy == nil || policy.Sigstore == nil || policy.Sigstore.Identity != miseSigner {
		t.Fatalf("signature policy = %+v; want the first keyless Signer pinned", policy)
	}
	if applied, _ := engine.OpenBaselines(scope.skills).Applied("mise"); !applied.Signed {
		t.Fatalf("Baseline = %+v; want it signed", applied)
	}

	// A tampered artifact is blocked while planning; the Scope copy stays.
	server.SetEntries(miseArchive(t, "---\nname: mise\ndescription: tampered\n---\n", true))
	out, err = scope.run(t, "update")
	if ExitCode(err) != 1 || !strings.Contains(out, "Skipped mise: signature:") || *fetches != 2 {
		t.Fatalf("update over a tampered artifact = %v (exit %d), %d fetches; want exit 1 and a fresh trust root\n%s", err, ExitCode(err), *fetches, out)
	}
	if got := scopeSkillMD(t, scope, "mise"); got != string(original) {
		t.Fatalf("a blocked update replaced the Scope copy: %q", got)
	}

	// Trusting records the entry digest, and Sync, offline, applies only it.
	out, err = scope.run(t, "trust", "mise", "--yes")
	if err != nil {
		t.Fatalf("trust: %v\n%s", err, out)
	}
	if tree, _ := wellKnownPolicy(t, scope, server).TrustedTree("mise"); tree != server.Entries[0].Digest {
		t.Fatalf("Trusted content = %q; want the entry digest %s", tree, server.Entries[0].Digest)
	}
	server.Close()
	out, err = scope.run(t, "sync")
	if err != nil || !strings.Contains(out, "Trusted unverified: mise") || *fetches != 2 {
		t.Fatalf("sync = %v, %d fetches; want the trusted copy applied offline\n%s", err, *fetches, out)
	}
	if got := scopeSkillMD(t, scope, "mise"); !strings.Contains(got, "tampered") {
		t.Fatalf("sync did not apply the trusted copy: %q", got)
	}

	out, err = scope.run(t, "trust", "--revoke", "mise")
	if err != nil {
		t.Fatalf("trust --revoke: %v\n%s", err, out)
	}
	if out, err := scope.run(t, "sync", "--dry-run", "--force"); ExitCode(err) != 1 {
		t.Fatalf("sync after revoke = %v; want it blocked again (exit 1)\n%s", err, out)
	}
}

func TestCLIDoctorReportsTrustForAWellKnownSkillTheSourceNoLongerHas(t *testing.T) {
	offlineTrustRoot(t)
	server := newWellKnownServer(t, miseArchive(t, "", true))
	scope := newWellKnownScope(t)
	if out, err := scope.run(t, "add", server.URL, "--skill", "mise", "-y"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	server.SetEntries(miseArchive(t, "---\nname: mise\ndescription: tampered\n---\n", true))
	if out, err := scope.run(t, "update"); ExitCode(err) != 1 {
		t.Fatalf("update = %v; want the tampered copy blocked\n%s", err, out)
	}
	if out, err := scope.run(t, "trust", "mise", "--yes"); err != nil {
		t.Fatalf("trust: %v\n%s", err, out)
	}

	server.SetEntries(miseArchive(t, "---\nname: mise\ndescription: tampered again\n---\n", true))
	if out, err := scope.run(t, "update"); ExitCode(err) != 1 {
		t.Fatalf("update = %v; want the new copy blocked too\n%s", err, out)
	}
	out, _ := scope.run(t, "doctor")
	if !strings.Contains(out, "Trusted content for mise no longer matches its Source.") {
		t.Fatalf("doctor should report the stale trust:\n%s", out)
	}
}

func TestCLIUpdateRefusesAWellKnownSkillThatArrivesUnsigned(t *testing.T) {
	offlineTrustRoot(t)
	server := newWellKnownServer(t, miseArchive(t, "", true))
	scope := newWellKnownScope(t)
	if out, err := scope.run(t, "add", server.URL, "--skill", "mise", "-y"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}

	server.SetEntries(miseArchive(t, "---\nname: mise\ndescription: unsigned\n---\n", false))
	out, err := scope.run(t, "update")
	if ExitCode(err) != 1 || !strings.Contains(out, "unsigned, but it was signed when last applied") {
		t.Fatalf("update = %v (exit %d); want the unsigned copy refused\n%s", err, ExitCode(err), out)
	}
	if got := scopeSkillMD(t, scope, "mise"); strings.Contains(got, "unsigned") {
		t.Fatalf("a refused update replaced the Scope copy: %q", got)
	}
}

func TestCLISyncBlocksASkillMDEntryWhenTheSourceRequiresSignatures(t *testing.T) {
	fetches := offlineTrustRoot(t)
	server := newWellKnownServer(t, wellknowntest.SkillMD("lint", "Lint"))
	scope := newWellKnownScope(t)
	if out, err := scope.run(t, "add", server.URL, "--skill", "lint", "-y"); err != nil || *fetches != 0 {
		t.Fatalf("add = %v, %d fetches; want exit 0 without reaching Sigstore\n%s", err, *fetches, out)
	}
	cfg, err := config.LoadConfig(scope.config)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.TrimPrefix(server.URL, "https://")
	repo := cfg.Remote[key]
	repo.Signature = &config.SignaturePolicy{Require: true}
	cfg.Remote[key] = repo
	if err := config.SaveConfig(cfg, scope.config); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(scope.skills, "lint")); err != nil {
		t.Fatal(err)
	}

	out, err := scope.run(t, "sync")
	if ExitCode(err) != 1 || !strings.Contains(out, "a single SKILL.md entry cannot carry a signature") {
		t.Fatalf("sync = %v (exit %d); want the skill-md entry blocked, naming why\n%s", err, ExitCode(err), out)
	}
}

func TestCLIAddPinsATrustCertForAWellKnownSource(t *testing.T) {
	server := newWellKnownServer(t, wellknowntest.SkillMD("lint", "Lint"))
	scope := newWellKnownScope(t)
	cert := filepath.Join(scope.root, "root.pem")
	pem, err := os.ReadFile(nvidiaRootCert)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cert, pem, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := scope.run(t, "add", server.URL, "--skill", "lint", "--trust-cert", cert, "-y")
	if err != nil {
		t.Fatalf("add --trust-cert: %v\n%s", err, out)
	}
	if policy := wellKnownPolicy(t, scope, server); policy == nil || policy.CertificateChain == "" {
		t.Fatalf("signature policy = %+v; want the certificate chain pinned", policy)
	}
}

func TestCLIAddBlocksATamperedWellKnownArchiveAndWritesNothing(t *testing.T) {
	offlineTrustRoot(t)
	server := newWellKnownServer(t, miseArchive(t, "---\nname: mise\ndescription: tampered\n---\n", true))
	scope := newWellKnownScope(t)

	out, err := scope.run(t, "add", server.URL, "--skill", "mise", "-y")
	if ExitCode(err) != 1 || !strings.Contains(out, "Skipped mise: signature:") {
		t.Fatalf("add = %v (exit %d); want a signature block (exit 1)\n%s", err, ExitCode(err), out)
	}
	if _, err := os.Stat(filepath.Join(scope.skills, "mise")); !os.IsNotExist(err) {
		t.Fatalf("a blocked add wrote the Scope copy: %v", err)
	}
}

func TestCLISyncRefusesAWellKnownSkillSignedByAnotherSigner(t *testing.T) {
	offlineTrustRoot(t)
	server := newWellKnownServer(t, miseArchive(t, "", true))
	scope := newWellKnownScope(t)
	if out, err := scope.run(t, "add", server.URL, "--skill", "mise", "-y"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	cfg, err := config.LoadConfig(scope.config)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.TrimPrefix(server.URL, "https://")
	cfg.Remote[key].Signature.Sigstore.Identity = "https://github.com/someone/else/.github/workflows/sign.yml@refs/heads/main"
	if err := config.SaveConfig(cfg, scope.config); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(scope.skills, "mise")); err != nil {
		t.Fatal(err)
	}

	out, err := scope.run(t, "sync")
	if ExitCode(err) != 1 || !strings.Contains(out, "Skipped mise: signature:") {
		t.Fatalf("sync = %v (exit %d); want a Skill from another Signer blocked\n%s", err, ExitCode(err), out)
	}
}
