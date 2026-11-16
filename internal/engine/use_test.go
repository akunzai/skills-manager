package engine

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/signing"
)

// An unsigned Skill is read from the Cache as is, and Sigstore is never
// reached for it.
func TestUseReadsAnUnsignedSkill(t *testing.T) {
	origin := filepath.Join(t.TempDir(), "origin")
	writeLocalGitSkill(t, origin, "sample")
	intake := mustPrepareRemoteIntake(t, config.DefaultConfig(), remoteSpec(origin, "", ""), filepath.Join(t.TempDir(), "cache"))
	fetches := stubTrustedRoot(t)

	used, err := intake.Use("sample", "")
	if err != nil {
		t.Fatal(err)
	}
	if string(used.Body) != "# Sample\n" || used.Signed || used.Signer != nil || *fetches != 0 {
		t.Fatalf("used = %+v, fetches %d; want the unsigned SKILL.md and no trust root", used, *fetches)
	}
}

// A keyless signature is checked against a freshly fetched trust root and any
// identity, and the Signer is returned rather than recorded (ADR-0010).
func TestUseVerifiesAKeylessSkillAndReportsItsSigner(t *testing.T) {
	cfg, _, _, _, intake := addSignedFixture(t)
	fetches := stubTrustedRoot(t)
	calls := stubVerify(t, signedBy(testSigner))

	used, err := intake.Use("sample", "")
	if err != nil {
		t.Fatal(err)
	}
	if string(used.Body) != "# Sample\n" || !used.Signed || used.Signer == nil || *used.Signer != testSigner {
		t.Fatalf("used = %+v; want the SKILL.md signed by %v", used, testSigner)
	}
	if *fetches != 1 || len(*calls) != 1 || (*calls)[0].Sigstore != nil {
		t.Fatalf("fetches %d, trust %+v; want one trust root fetch and any identity accepted", *fetches, *calls)
	}
	if len(cfg.Remote) != 0 {
		t.Fatalf("Config = %+v; Use must not record anything", cfg.Remote)
	}
}

func TestUseRefusesASkillThatDoesNotVerify(t *testing.T) {
	_, _, _, _, intake := addSignedFixture(t)
	stubTrustedRoot(t)
	stubVerify(t, func(string, signing.Trust) (signing.Result, error) {
		return signing.Result{}, errors.New("digest mismatch for SKILL.md")
	})

	used, err := intake.Use("sample", "")
	var blocked *UseBlockedError
	if !errors.As(err, &blocked) || used.Body != nil {
		t.Fatalf("used = %+v, err = %v; want a UseBlockedError with nothing read", used, err)
	}
}

func TestUseRefusesASignedSkillWithoutATrustRoot(t *testing.T) {
	_, _, _, _, intake := addSignedFixture(t)
	failingTrustedRoot(t)
	stubVerify(t, signedBy(testSigner))

	used, err := intake.Use("sample", "")
	var blocked *UseBlockedError
	if err == nil || errors.As(err, &blocked) || used.Body != nil {
		t.Fatalf("used = %+v, err = %v; want a failure, not a block, when the trust root cannot be fetched", used, err)
	}
}

// --trust-cert checks a certificate chain instead, and needs no trust root.
func TestUseVerifiesACertificateChainAgainstTrustCert(t *testing.T) {
	_, _, _, _, intake := addSignedFixture(t)
	fetches := stubTrustedRoot(t)
	calls := stubVerify(t, func(string, signing.Trust) (signing.Result, error) {
		return signing.Result{Signed: true}, nil
	})
	pem, err := os.ReadFile(filepath.Join("..", "signing", "testdata", "nvidia", "nv-agent-root-cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(t.TempDir(), "root.pem")
	if err := os.WriteFile(certPath, pem, 0o644); err != nil {
		t.Fatal(err)
	}

	used, err := intake.Use("sample", certPath)
	if err != nil {
		t.Fatal(err)
	}
	if !used.Signed || used.Signer != nil || len(*calls) != 1 || (*calls)[0].Roots == nil || *fetches != 0 {
		t.Fatalf("used = %+v, trust %+v, fetches %d; want a chain verified against the certificate", used, *calls, *fetches)
	}
}

func TestUseReportsASkillWithoutSkillFile(t *testing.T) {
	origin := filepath.Join(t.TempDir(), "origin")
	writeLocalGitSkill(t, origin, "sample")
	intake := mustPrepareRemoteIntake(t, config.DefaultConfig(), remoteSpec(origin, "", ""), filepath.Join(t.TempDir(), "cache"))

	var blocked *UseBlockedError
	if _, err := intake.Use("elsewhere", ""); !errors.As(err, &blocked) {
		t.Fatalf("err = %v; want a UseBlockedError for a subpath with no SKILL.md", err)
	}
}
