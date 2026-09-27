package engine

import (
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/signing"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
)

// SyncBlockSignature is a remote Skill whose signature does not verify, or
// that its Source's policy requires to be signed and is not. No decision lifts
// it: --force overwrites local edits, never an unverified Source.
const SyncBlockSignature SyncBlock = "signature"

// SyncSignerRecorded is the first keyless signer seen for a Source, written
// to Config so later Skills must match it.
const SyncSignerRecorded = "signer_recorded"

// SyncSignerFailed is a signer Sync could not record because Config could not
// be saved.
const SyncSignerFailed = "signer_failed"

// trustRootDir holds the Sigstore trust root under the Cache directory. A
// Source key cannot start with a dot, so it never collides with a Cache.
func trustRootDir(cacheDir string) string {
	return filepath.Join(cacheDirOrDefault(cacheDir), ".sigstore")
}

func trustRootPath(cacheDir string) string {
	return filepath.Join(trustRootDir(cacheDir), "trusted_root.json")
}

// verifySkill is signing.Verify; engine tests replace it to exercise the
// policy without real signatures, which internal/signing's tests cover.
var verifySkill = signing.Verify

// fetchTrustedRoot fetches the Sigstore public-good trust root through TUF.
// Tests replace it: they must not reach the network.
var fetchTrustedRoot = func(cacheDir string) ([]byte, error) {
	opts := tuf.DefaultOptions().WithCachePath(filepath.Join(trustRootDir(cacheDir), "tuf"))
	trusted, err := root.FetchTrustedRootWithOptions(opts)
	if err != nil {
		return nil, err
	}
	return trusted.MarshalJSON()
}

// RefreshTrustRoot fetches the Sigstore trust root into the Cache directory,
// where Sync reads it without network access (ADR-0004). It runs only where a
// Cache was just refreshed and a Skill in it is signed, so Update and Add of
// unsigned Sources never reach Sigstore.
func RefreshTrustRoot(cacheDir string) error {
	data, err := fetchTrustedRoot(cacheDir)
	if err != nil {
		return fmt.Errorf("fetch Sigstore trust root: %w", err)
	}
	if err := os.MkdirAll(trustRootDir(cacheDir), 0o755); err != nil {
		return err
	}
	tmp := trustRootPath(cacheDir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, trustRootPath(cacheDir))
}

// refreshTrustRootFor refreshes the trust root when a Skill at subpaths of
// cache is signed. A failure is not reported here: verifying that Skill then
// names Update as the way out.
func refreshTrustRootFor(cache Cache, subpaths []string) error {
	dirs := make([]string, 0, len(subpaths))
	for _, subpath := range subpaths {
		dirs = append(dirs, filepath.Join(cache.dir(), filepath.FromSlash(subpath)))
	}
	if !anySigned(dirs...) {
		return nil
	}
	return RefreshTrustRoot(cache.cacheDir)
}

// anySigned reports whether a Skill directory under any of dirs carries a
// signature, so the trust root is worth fetching.
func anySigned(dirs ...string) bool {
	for _, dir := range dirs {
		if _, err := os.Stat(filepath.Join(dir, signing.FileName)); err == nil {
			return true
		}
	}
	return false
}

// signatures verifies remote Skills against their Source's signature policy
// for one plan. It loads the trust root once, and remembers the first keyless
// signer seen for a Source with no pinned signer, so every Skill of that
// Source in the plan is held to the same one.
type signatures struct {
	cfg       *config.Config
	configDir string
	cacheDir  string
	baselines *Baselines

	trustedRoot root.TrustedMaterial
	loaded      bool
	observed    map[string]signing.Signer
}

func newSignatures(cfg *config.Config, configPath, cacheDir string, baselines *Baselines) *signatures {
	return &signatures{
		cfg:       cfg,
		configDir: filepath.Dir(configPath),
		cacheDir:  cacheDir,
		baselines: baselines,
		observed:  make(map[string]signing.Signer),
	}
}

func (s *signatures) root() root.TrustedMaterial {
	if !s.loaded {
		s.loaded = true
		if trusted, err := root.NewTrustedRootFromPath(trustRootPath(s.cacheDir)); err == nil {
			s.trustedRoot = trusted
		}
	}
	return s.trustedRoot
}

// check verifies item's Skill in its Cache and records the verdict on item: a
// SyncBlockSignature block, whether it is signed, and the signer to record
// for a Source that has none.
func (s *signatures) check(item *SyncPlanItem) {
	policy := s.cfg.Remote[item.Source].Signature
	trust, err := s.trust(item.Source, policy)
	if err != nil {
		s.block(item, err.Error(), "")
		return
	}
	result, err := verifySkill(filepath.Join(item.CachePath, filepath.FromSlash(item.Freshness.Subpath)), trust)
	if errors.Is(err, signing.ErrNoTrustRoot) {
		s.block(item, "signed, but "+err.Error(), "update")
		return
	}
	if err != nil {
		s.block(item, err.Error(), "")
		return
	}
	if !result.Signed {
		applied, _ := s.baselines.Applied(item.Name)
		switch {
		case policy != nil && policy.Require:
			s.block(item, fmt.Sprintf("unsigned, and Source %s requires signatures", item.Source), "")
		case applied.Signed:
			s.block(item, "unsigned, but it was signed when last applied", "")
		}
		return
	}
	item.Signed = true
	if !policy.Pinned() && result.Signer != (signing.Signer{}) {
		if _, seen := s.observed[item.Source]; !seen {
			s.observed[item.Source] = result.Signer
		}
		signer := s.observed[item.Source]
		item.RecordSigner = &signer
	}
}

func (s *signatures) block(item *SyncPlanItem, reason, next string) {
	item.Block = SyncBlockSignature
	item.BlockReason = reason
	item.BlockNext = next
	item.Signed = false
	item.RecordSigner = nil
}

// trust is what policy trusts for source, falling back to the first keyless
// signer this plan saw for it.
func (s *signatures) trust(source string, policy *config.SignaturePolicy) (signing.Trust, error) {
	if policy != nil && policy.CertificateChain != "" {
		path := policy.CertificateChain
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.configDir, path)
		}
		pem, err := os.ReadFile(path)
		if err != nil {
			return signing.Trust{}, fmt.Errorf("read trusted certificate: %w", err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return signing.Trust{}, fmt.Errorf("no certificate in %s", policy.CertificateChain)
		}
		return signing.Trust{Roots: roots}, nil
	}
	trust := signing.Trust{TrustedRoot: s.root()}
	if policy != nil && policy.Sigstore != nil {
		trust.Sigstore = &signing.Signer{Identity: policy.Sigstore.Identity, Issuer: policy.Sigstore.Issuer}
	} else if signer, ok := s.observed[source]; ok {
		trust.Sigstore = &signer
	}
	return trust, nil
}

// checkable reports whether item is a remote Skill whose Cache copy is about
// to be read: one with no other block or error, and not a rename, whose new
// Skill is checked when the rename is applied.
func checkable(item SyncPlanItem) bool {
	return item.Kind == config.SkillRemote && item.Block == SyncBlockNone && item.Err == "" && item.Freshness.Status != SkillRenamed
}

// recordSigner writes the keyless signer items saw for source to cfg, when
// the Source has none pinned. It reports whether cfg changed.
func recordSigner(cfg *config.Config, source string, items []SyncPlanItem) (*signing.Signer, bool) {
	repo, ok := cfg.Remote[source]
	if !ok || repo.Signature.Pinned() {
		return nil, false
	}
	for _, item := range items {
		if item.RecordSigner == nil {
			continue
		}
		policy := config.SignaturePolicy{}
		if repo.Signature != nil {
			policy = *repo.Signature
		}
		policy.Sigstore = &config.SigstoreSigner{Identity: item.RecordSigner.Identity, Issuer: item.RecordSigner.Issuer}
		repo.Signature = &policy
		cfg.Remote[source] = repo
		return item.RecordSigner, true
	}
	return nil, false
}
