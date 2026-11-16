package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/akunzai/skills-manager/internal/signing"
)

// UseBlockedError is a Skill Use reached but will not hand over: its
// signature does not verify, or it has no SKILL.md. It is the Skill's state,
// not a failure to read it (ADR-0002).
type UseBlockedError struct{ Reason string }

func (e *UseBlockedError) Error() string { return e.Reason }

// UsedSkill is a Skill's SKILL.md as Use read it from the Cache.
type UsedSkill struct {
	Body []byte
	// Signed is whether the Skill carried a signature that verified.
	Signed bool
	// Signer is the keyless identity that signed it; nil when unsigned or
	// signed with a certificate chain.
	Signer *signing.Signer
}

// Use reads the SKILL.md of the Skill at subpath without declaring it: it
// covers the Skill in the Cache and verifies it there first, as every
// command that hands a remote Skill to an Agent does (ADR-0010). Nothing is
// recorded, so a keyless signature is accepted from any identity and the
// Signer is returned for the caller to show. trustCert, when set, is a PEM
// file the signature must chain to instead. A Skill that does not verify or
// has no SKILL.md is a *UseBlockedError; any other error is a failure to
// read it, such as a trust root that could not be fetched.
func (in *RemoteIntake) Use(subpath, trustCert string) (UsedSkill, error) {
	if err := in.cache.Cover(subpath); err != nil {
		return UsedSkill{}, fmt.Errorf("fetch %s into the Cache: %w", subpath, err)
	}
	dir := filepath.Join(in.cache.dir(), filepath.FromSlash(subpath))
	trust := signing.Trust{}
	if trustCert != "" {
		var err error
		if trust, err = certificateChainTrust(trustCert, trustCert); err != nil {
			return UsedSkill{}, err
		}
	} else {
		if err := ensureTrustRoot(in.cacheDir, true, dir); err != nil {
			return UsedSkill{}, err
		}
		if anySigned(dir) {
			if trusted, err := root.NewTrustedRootFromPath(trustRootPath(in.cacheDir)); err == nil {
				trust.TrustedRoot = trusted
			}
		}
	}
	result, err := verifySkill(dir, trust)
	if err != nil {
		return UsedSkill{}, &UseBlockedError{Reason: "its signature does not verify: " + err.Error()}
	}
	body, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if errors.Is(err, fs.ErrNotExist) {
		return UsedSkill{}, &UseBlockedError{Reason: "it has no SKILL.md"}
	}
	if err != nil {
		return UsedSkill{}, err
	}
	used := UsedSkill{Body: body, Signed: result.Signed}
	if result.Signer != (signing.Signer{}) {
		used.Signer = &result.Signer
	}
	return used, nil
}
