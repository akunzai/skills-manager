package signing

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// nvidiaSigned is the time the NVIDIA fixture is checked at, inside its
// signing certificate's validity, so the test does not expire with it.
var nvidiaSigned = time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)

// nvidiaSkill copies the NVIDIA fixture Skill somewhere a test may change it.
func nvidiaSkill(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "mcore-linting-and-formatting")
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("testdata", "nvidia", "mcore-linting-and-formatting"))); err != nil {
		t.Fatal(err)
	}
	return dir
}

func nvidiaTrust(t *testing.T) Trust {
	t.Helper()
	pem, err := os.ReadFile(filepath.Join("testdata", "nvidia", "nv-agent-root-cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("no certificate in nv-agent-root-cert.pem")
	}
	return Trust{Roots: roots, Now: nvidiaSigned}
}

func TestVerifyCertificateChain(t *testing.T) {
	result, err := Verify(nvidiaSkill(t), nvidiaTrust(t))
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !result.Signed || result.Signer != (Signer{}) {
		t.Fatalf("Verify() = %+v, want signed with no keyless signer", result)
	}
}

func TestVerifyUnsigned(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Verify(dir, Trust{})
	if err != nil || result.Signed {
		t.Fatalf("Verify() = %+v, %v; want unsigned, nil", result, err)
	}
}

func TestVerifyRejectsChangedTree(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, dir string)
		want   string
	}{
		{"changed file", func(t *testing.T, dir string) {
			appendFile(t, filepath.Join(dir, "SKILL.md"), "tampered\n")
		}, "SKILL.md changed after signing"},
		{"extra file", func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, "extra.md"), "x\n")
		}, "extra.md was not signed"},
		{"missing file", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "BENCHMARK.md")); err != nil {
				t.Fatal(err)
			}
		}, "BENCHMARK.md is signed but missing"},
		{"symlink", func(t *testing.T, dir string) {
			if err := os.Symlink("SKILL.md", filepath.Join(dir, "link.md")); err != nil {
				t.Skip("symlinks unavailable:", err)
			}
		}, "link.md is a symlink"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := nvidiaSkill(t)
			tc.change(t, dir)
			_, err := Verify(dir, nvidiaTrust(t))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Verify() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestVerifyHonoursSignedIgnorePaths(t *testing.T) {
	dir := nvidiaSkill(t)
	// The fixture's predicate ignores .github, .git, .gitattributes, and
	// .gitignore.
	writeFile(t, filepath.Join(dir, ".github", "workflow.yml"), "x\n")
	writeFile(t, filepath.Join(dir, ".gitignore"), "x\n")
	if _, err := Verify(dir, nvidiaTrust(t)); err != nil {
		t.Fatalf("Verify() error = %v, want ignored paths accepted", err)
	}
}

func TestVerifyRejectsUntrustedRoot(t *testing.T) {
	trust := nvidiaTrust(t)
	trust.Roots = x509.NewCertPool()
	trust.Roots.AddCert(selfSignedCA(t))
	_, err := Verify(nvidiaSkill(t), trust)
	if err == nil || !strings.Contains(err.Error(), "does not chain to the trusted certificate") {
		t.Fatalf("Verify() error = %v, want an untrusted chain", err)
	}
}

func TestVerifyRejectsExpiredChain(t *testing.T) {
	trust := nvidiaTrust(t)
	trust.Now = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := Verify(nvidiaSkill(t), trust); err == nil {
		t.Fatal("Verify() error = nil, want an expired signing certificate")
	}
}

func TestVerifyChainSignedSkillWithoutTrustedCertificate(t *testing.T) {
	_, err := Verify(nvidiaSkill(t), Trust{})
	if err == nil || !strings.Contains(err.Error(), "--trust-cert") {
		t.Fatalf("Verify() error = %v, want a pointer to --trust-cert", err)
	}
	if errors.Is(err, ErrNoTrustRoot) {
		t.Fatal("a certificate-chain signature does not need a Sigstore trust root")
	}
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func selfSignedCA(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Untrusted CA"},
		NotBefore:             time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
