package config

import (
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/akunzai/skills-manager/internal/models"
)

// StoreTrustCertPath checks that path holds a PEM certificate and returns it
// as Config stores a Source's certificate-chain trust anchor: relative to the
// directory holding Config when inside it, so a Project Config stays
// portable, and absolute otherwise. It is the trust-certificate counterpart
// of models.StoreLocalSourcePath / models.ResolveLocalSourcePath, and the
// PEM is validated here, once, rather than again when it is read back.
func StoreTrustCertPath(path, configPath string) (string, error) {
	if path == "" {
		return "", nil
	}
	abs, err := filepath.Abs(models.ExpandUser(path))
	if err != nil {
		return "", err
	}
	pem, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("read --trust-cert: %w", err)
	}
	if !x509.NewCertPool().AppendCertsFromPEM(pem) {
		return "", fmt.Errorf("--trust-cert %s holds no PEM certificate", path)
	}
	configDir, err := filepath.Abs(filepath.Dir(configPath))
	if err != nil {
		return abs, nil
	}
	if rel, err := filepath.Rel(configDir, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel), nil
	}
	return abs, nil
}

// ResolveTrustCertPath turns a Config-stored certificate-chain path back into
// one that can be read: a relative path resolves against the directory
// holding Config, an absolute one (or an empty one) is returned unchanged.
func ResolveTrustCertPath(path, configPath string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(filepath.Dir(configPath), filepath.FromSlash(path))
}

// SetCertificateChainTrust sets source's signature policy to trust the
// certificate chain at path, as StoreTrustCertPath returns it. Sigstore and
// CertificateChain are exclusive, so any pinned Sigstore signer is cleared.
func SetCertificateChainTrust(cfg *Config, source, path string) {
	repo := cfg.Remote[source]
	policy := SignaturePolicy{}
	if repo.Signature != nil {
		policy = *repo.Signature
	}
	policy.Sigstore, policy.CertificateChain = nil, path
	repo.Signature = &policy
	cfg.Remote[source] = repo
}
