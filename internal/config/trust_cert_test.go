package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreTrustCertPathInsideConfigDirIsRelative(t *testing.T) {
	pem, err := os.ReadFile(filepath.Join("..", "signing", "testdata", "nvidia", "nv-agent-root-cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	configPath := filepath.Join(project, ".agents", "skills.json")
	inside := filepath.Join(project, ".agents", "trust", "root.pem")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, pem, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := StoreTrustCertPath(inside, configPath)
	if err != nil || got != "trust/root.pem" {
		t.Fatalf("StoreTrustCertPath(inside) = %q, %v; want trust/root.pem", got, err)
	}
}

func TestStoreTrustCertPathOutsideConfigDirIsAbsolute(t *testing.T) {
	pem, err := os.ReadFile(filepath.Join("..", "signing", "testdata", "nvidia", "nv-agent-root-cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), ".agents", "skills.json")
	outside := filepath.Join(t.TempDir(), "root.pem")
	if err := os.WriteFile(outside, pem, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := StoreTrustCertPath(outside, configPath)
	if err != nil || !filepath.IsAbs(got) {
		t.Fatalf("StoreTrustCertPath(outside) = %q, %v; want an absolute path", got, err)
	}
}

func TestStoreTrustCertPathRefusesAFileWithNoPEMCertificate(t *testing.T) {
	project := t.TempDir()
	configPath := filepath.Join(project, ".agents", "skills.json")
	notPEM := filepath.Join(project, "not.pem")
	if err := os.WriteFile(notPEM, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := StoreTrustCertPath(notPEM, configPath); err == nil {
		t.Fatal("StoreTrustCertPath should refuse a file with no PEM certificate")
	}
}

func TestStoreTrustCertPathEmptyIsEmpty(t *testing.T) {
	got, err := StoreTrustCertPath("", filepath.Join(t.TempDir(), "skills.json"))
	if err != nil || got != "" {
		t.Fatalf("StoreTrustCertPath(empty) = %q, %v; want empty, no error", got, err)
	}
}

func TestResolveTrustCertPathRelativeResolvesAgainstConfigDir(t *testing.T) {
	configPath := filepath.Join("project", ".agents", "skills.json")
	got := ResolveTrustCertPath("trust/root.pem", configPath)
	want := filepath.Join("project", ".agents", "trust", "root.pem")
	if got != want {
		t.Fatalf("ResolveTrustCertPath(relative) = %q; want %q", got, want)
	}
}

func TestResolveTrustCertPathAbsoluteReturnedAsIs(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "root.pem")
	if got := ResolveTrustCertPath(abs, "/anything/skills.json"); got != abs {
		t.Fatalf("ResolveTrustCertPath(absolute) = %q; want %q", got, abs)
	}
}

func TestResolveTrustCertPathEmptyIsEmpty(t *testing.T) {
	if got := ResolveTrustCertPath("", "/anything/skills.json"); got != "" {
		t.Fatalf("ResolveTrustCertPath(empty) = %q; want empty", got)
	}
}

func TestStoreThenResolveTrustCertPathRoundTrips(t *testing.T) {
	pem, err := os.ReadFile(filepath.Join("..", "signing", "testdata", "nvidia", "nv-agent-root-cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	configPath := filepath.Join(project, ".agents", "skills.json")
	original := filepath.Join(project, ".agents", "trust", "root.pem")
	if err := os.MkdirAll(filepath.Dir(original), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, pem, 0o644); err != nil {
		t.Fatal(err)
	}

	stored, err := StoreTrustCertPath(original, configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := ResolveTrustCertPath(stored, configPath); got != original {
		t.Fatalf("ResolveTrustCertPath(StoreTrustCertPath(x)) = %q; want %q", got, original)
	}
}

func TestSetCertificateChainTrustClearsAnyPinnedSigstoreSigner(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Remote["owner/repo"] = RemoteRepo{
		Type: "github",
		Signature: &SignaturePolicy{
			Require:  true,
			Sigstore: &SigstoreSigner{Identity: "someone@example.com", Issuer: "https://issuer"},
		},
	}

	SetCertificateChainTrust(cfg, "owner/repo", "root.pem")

	repo := cfg.Remote["owner/repo"]
	if repo.Signature == nil || repo.Signature.CertificateChain != "root.pem" || repo.Signature.Sigstore != nil {
		t.Fatalf("signature = %+v, want certificateChain root.pem and no Sigstore signer", repo.Signature)
	}
	if !repo.Signature.Require {
		t.Fatal("SetCertificateChainTrust must not touch the Require flag")
	}
}

func TestSetCertificateChainTrustOnASourceWithNoPolicyYet(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Remote["owner/repo"] = RemoteRepo{Type: "github"}

	SetCertificateChainTrust(cfg, "owner/repo", "root.pem")

	repo := cfg.Remote["owner/repo"]
	if repo.Signature == nil || repo.Signature.CertificateChain != "root.pem" {
		t.Fatalf("signature = %+v, want certificateChain root.pem", repo.Signature)
	}
}
