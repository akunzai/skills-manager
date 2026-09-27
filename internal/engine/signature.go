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

// SyncTrustedUnverified is a Skill Materialized as Trusted content: its
// signature does not verify, and the user trusted this exact content.
const SyncTrustedUnverified = "trusted_unverified"

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

// remotePlanner plans the remote Skills of one operation — a Sync, an Add,
// an Adopt — and verifies each one's Cache copy against its Source's signature
// policy as it plans it, so no caller holds a remote item that was not
// verified (ADR-0010). It loads the trust root once found, and remembers the first
// keyless signer seen for a Source with no pinned signer, so every Skill of
// that Source in the operation is held to the same one and recordSigner can
// write it to Config.
type remotePlanner struct {
	cfg       *config.Config
	configDir string
	cacheDir  string
	baselines *Baselines

	trustedRoot root.TrustedMaterial
	observed    map[string]signing.Signer
}

func newRemotePlanner(cfg *config.Config, configPath, cacheDir string, baselines *Baselines) *remotePlanner {
	return &remotePlanner{
		cfg:       cfg,
		configDir: filepath.Dir(configPath),
		cacheDir:  cacheDir,
		baselines: baselines,
		observed:  make(map[string]signing.Signer),
	}
}

// reconcile plans one declared remote Skill from its classified Freshness:
// Sync reconciling an existing declaration. A renamed Skill carries its new
// Skill, verified now, so a rename to a Skill that does not verify is blocked
// before the old one is Retired.
func (p *remotePlanner) reconcile(skillsDir, source string, cache Cache, skill SkillFreshness, drift AvailabilityDrift) SyncPlanItem {
	item := planRemoteItem(source, cache, skill, drift)
	if skill.Status != SkillRenamed {
		// A Skill blocked or failed already is not read, so it keeps that
		// reason.
		if item.Block == SyncBlockNone && item.Err == "" {
			p.verify(&item)
		}
		return item
	}
	item = planRename(p.cfg, skillsDir, item)
	if item.RenameTargetDeclared || item.Block == SyncBlockRenameOccupied {
		return item
	}
	// The new Skill is planned even when the old copy's Drift blocks the
	// rename, since --force lifts that; its signature no decision lifts.
	renamed := p.declared(source, cache, SkillFreshness{
		Name:      skill.RenamedTo,
		Source:    source,
		Subpath:   skill.RenamedSubpath,
		ScopePath: filepath.Join(skillsDir, skill.RenamedTo),
	}, AvailabilityDrift{})
	if renamed.Block != SyncBlockNone {
		item.Block, item.BlockReason, item.BlockNext = renamed.Block, renamed.BlockReason, renamed.BlockNext
		return item
	}
	item.renamed = &renamed
	return item
}

// declared plans a remote Skill that was just declared, by Add or as the new
// name of a Rename. It is always Materialized unless its signature blocks it:
// neither Drift nor a missing Baseline does, because Add asked before
// overwriting and a Rename already protected the old copy. skill carries no
// Freshness status.
func (p *remotePlanner) declared(source string, cache Cache, skill SkillFreshness, drift AvailabilityDrift) SyncPlanItem {
	item := baseRemoteItem(source, cache, skill, drift)
	item.NeedsWrite = true
	p.verify(&item)
	return item
}

// recorded plans a remote Skill whose copy on the Scope skills directory
// already matches the Cache, as Adopt finds one: applying it writes nothing
// and records the Baseline.
func (p *remotePlanner) recorded(source string, cache Cache, skill SkillFreshness, drift AvailabilityDrift) SyncPlanItem {
	item := baseRemoteItem(source, cache, skill, drift)
	p.verify(&item)
	return item
}

// recordSigner writes the first keyless signer planned for source to Config,
// when the Source is declared with none pinned. It reports whether Config
// changed; saving it is the caller's. A plan built without a planner has
// planned no signer.
func (p *remotePlanner) recordSigner(source string) (*signing.Signer, bool) {
	if p == nil {
		return nil, false
	}
	signer, seen := p.observed[source]
	repo, declared := p.cfg.Remote[source]
	if !seen || !declared || repo.Signature.Pinned() {
		return nil, false
	}
	policy := config.SignaturePolicy{}
	if repo.Signature != nil {
		policy = *repo.Signature
	}
	policy.Sigstore = &config.SigstoreSigner{Identity: signer.Identity, Issuer: signer.Issuer}
	repo.Signature = &policy
	p.cfg.Remote[source] = repo
	return &signer, true
}

// root loads the trust root once it is there. A missing one is looked for
// again: Adopt declares several Sources in one operation, and the Remote
// intake of a later, signed one fetches it.
func (p *remotePlanner) root() root.TrustedMaterial {
	if p.trustedRoot == nil {
		if trusted, err := root.NewTrustedRootFromPath(trustRootPath(p.cacheDir)); err == nil {
			p.trustedRoot = trusted
		}
	}
	return p.trustedRoot
}

// verify checks item's Skill in its Cache and records the verdict on item: a
// SyncBlockSignature block, or whether it is signed.
func (p *remotePlanner) verify(item *SyncPlanItem) {
	policy := p.cfg.Remote[item.Source].Signature
	trust, err := p.trust(item.Source, policy)
	if err != nil {
		p.block(item, err.Error(), "")
		return
	}
	result, err := verifySkill(filepath.Join(item.CachePath, filepath.FromSlash(item.Freshness.Subpath)), trust)
	if errors.Is(err, signing.ErrNoTrustRoot) {
		p.block(item, "signed, but "+err.Error(), "update")
		return
	}
	if err != nil {
		p.fail(item, err.Error())
		return
	}
	if !result.Signed {
		applied, _ := p.baselines.Applied(item.Name)
		switch {
		case policy != nil && policy.Require:
			p.fail(item, fmt.Sprintf("unsigned, and Source %s requires signatures", item.Source))
		case applied.Signed:
			p.fail(item, "unsigned, but it was signed when last applied")
		}
		return
	}
	item.Signed = true
	if !policy.Pinned() && result.Signer != (signing.Signer{}) {
		if _, seen := p.observed[item.Source]; !seen {
			p.observed[item.Source] = result.Signer
		}
	}
}

// fail is a verdict against item's Cache copy. Trusted content for exactly
// this copy lets it through, marked unverified; otherwise it is blocked, and
// Trusted content for another copy names trust as the way out.
func (p *remotePlanner) fail(item *SyncPlanItem, reason string) {
	tree := item.cache.atHead(item.Freshness.Subpath).id
	trusted, ok := p.cfg.Remote[item.Source].Signature.TrustedTree(item.Name)
	if ok && tree != "" && trusted == tree {
		item.Signed, item.Unverified = false, reason
		return
	}
	next := ""
	if ok {
		next = "trust " + item.Name
	}
	p.block(item, reason, next)
	item.tree = tree
}

func (p *remotePlanner) block(item *SyncPlanItem, reason, next string) {
	item.Block = SyncBlockSignature
	item.BlockReason = reason
	item.BlockNext = next
	item.Signed = false
}

// trust is what policy trusts for source, falling back to the first keyless
// signer this operation saw for it.
func (p *remotePlanner) trust(source string, policy *config.SignaturePolicy) (signing.Trust, error) {
	if policy != nil && policy.CertificateChain != "" {
		path := policy.CertificateChain
		if !filepath.IsAbs(path) {
			path = filepath.Join(p.configDir, path)
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
	trust := signing.Trust{TrustedRoot: p.root()}
	if policy != nil && policy.Sigstore != nil {
		trust.Sigstore = &signing.Signer{Identity: policy.Sigstore.Identity, Issuer: policy.Sigstore.Issuer}
	} else if signer, ok := p.observed[source]; ok {
		trust.Sigstore = &signer
	}
	return trust, nil
}
