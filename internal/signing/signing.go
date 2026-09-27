// Package signing verifies a Skill directory against its detached OpenSSF
// Model Signing (OMS) signature, skill.oms.sig, the format NVIDIA/skills and
// akunzai/agent-skills publish and the Python model_signing CLI verifies.
//
// An OMS signature is a Sigstore bundle whose DSSE envelope carries an in-toto
// statement. Its predicate lists every signed file with a digest, plus the
// paths the signer ignored. Verify checks the envelope's signature, then
// recomputes every file's digest; a file on disk that is neither listed nor
// ignored fails, as model_signing's strict mode does.
package signing

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// FileName is the detached signature at a Skill's root. It is never part of
// what it signs.
const FileName = "skill.oms.sig"

const (
	predicateType   = "https://model_signing/signature/v1.0"
	inTotoMediaType = "application/vnd.in-toto+json"
)

// Signer is a Sigstore keyless signing identity: the certificate's subject
// alternative name and the OIDC issuer that vouched for it.
type Signer struct {
	Identity string
	Issuer   string
}

// Trust is what a Source trusts. With Roots set, a Skill must be signed with a
// certificate chain to one of them. Otherwise it must be signed keyless under
// TrustedRoot, by Sigstore when set, or by any identity when nil so the caller
// can record the one it sees.
type Trust struct {
	Sigstore    *Signer
	TrustedRoot root.TrustedMaterial
	Roots       *x509.CertPool
	// Now is the time certificate chains are checked at; zero means now.
	Now time.Time
}

// Result is what Verify found. An unsigned Skill is not an error: whether it
// may be Materialized is the caller's policy.
type Result struct {
	Signed bool
	// Signer is the keyless identity that signed; empty for a certificate
	// chain.
	Signer Signer
}

// ErrNoTrustRoot is a keyless signature with no Sigstore trust root to check
// it against.
var ErrNoTrustRoot = errors.New("no Sigstore trust root is cached")

// Verify checks dir against dir/FileName under trust.
func Verify(dir string, trust Trust) (Result, error) {
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return Result{}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("read %s: %w", FileName, err)
	}
	var raw rawBundle
	if err := json.Unmarshal(data, &raw); err != nil {
		return Result{}, fmt.Errorf("parse %s: %w", FileName, err)
	}
	chain := len(raw.VerificationMaterial.X509CertificateChain.Certificates) > 0

	var payload []byte
	var signer Signer
	switch {
	case trust.Roots != nil && !chain:
		return Result{}, errors.New("signed with Sigstore, but the Source trusts a certificate chain")
	case trust.Roots != nil:
		payload, err = verifyChain(raw, trust)
	case chain:
		return Result{}, errors.New("signed with a certificate chain; add the Source with --trust-cert to trust it")
	default:
		payload, signer, err = verifyKeyless(data, trust)
	}
	if err != nil {
		return Result{}, err
	}
	if err := verifyFiles(dir, payload); err != nil {
		return Result{}, err
	}
	return Result{Signed: true, Signer: signer}, nil
}

type rawBundle struct {
	VerificationMaterial struct {
		X509CertificateChain struct {
			Certificates []struct {
				RawBytes string `json:"rawBytes"`
			} `json:"certificates"`
		} `json:"x509CertificateChain"`
	} `json:"verificationMaterial"`
	DsseEnvelope struct {
		Payload     string `json:"payload"`
		PayloadType string `json:"payloadType"`
		Signatures  []struct {
			Sig string `json:"sig"`
		} `json:"signatures"`
	} `json:"dsseEnvelope"`
}

// verifyKeyless checks a Sigstore keyless bundle: certificate, transparency
// log entry, and envelope signature, then the signer's identity.
func verifyKeyless(data []byte, trust Trust) ([]byte, Signer, error) {
	if trust.TrustedRoot == nil {
		return nil, Signer{}, ErrNoTrustRoot
	}
	var b bundle.Bundle
	if err := b.UnmarshalJSON(data); err != nil {
		return nil, Signer{}, fmt.Errorf("parse %s: %w", FileName, err)
	}
	verifier, err := verify.NewVerifier(trust.TrustedRoot, verify.WithTransparencyLog(1), verify.WithIntegratedTimestamps(1))
	if err != nil {
		return nil, Signer{}, err
	}
	identity := verify.WithoutIdentitiesUnsafe()
	if trust.Sigstore != nil {
		certID, err := verify.NewShortCertificateIdentity(trust.Sigstore.Issuer, "", trust.Sigstore.Identity, "")
		if err != nil {
			return nil, Signer{}, err
		}
		identity = verify.WithCertificateIdentity(certID)
	}
	result, err := verifier.Verify(&b, verify.NewPolicy(verify.WithoutArtifactUnsafe(), identity))
	if err != nil {
		return nil, Signer{}, fmt.Errorf("signature does not verify: %w", err)
	}
	var signer Signer
	if result.Signature != nil && result.Signature.Certificate != nil {
		signer = Signer{
			Identity: result.Signature.Certificate.SubjectAlternativeName,
			Issuer:   result.Signature.Certificate.Extensions.Issuer,
		}
	}
	envelope, err := b.Envelope()
	if err != nil {
		return nil, Signer{}, fmt.Errorf("parse %s: %w", FileName, err)
	}
	if envelope.PayloadType != inTotoMediaType {
		return nil, Signer{}, fmt.Errorf("unexpected payload type %q", envelope.PayloadType)
	}
	payload, err := base64.StdEncoding.DecodeString(envelope.Payload)
	if err != nil {
		return nil, Signer{}, fmt.Errorf("decode payload: %w", err)
	}
	return payload, signer, nil
}

// verifyChain checks a bundle signed with a certificate chain: the leaf chains
// to one of trust.Roots through the bundle's intermediates, and signed the
// DSSE envelope.
func verifyChain(raw rawBundle, trust Trust) ([]byte, error) {
	var certs []*x509.Certificate
	for _, c := range raw.VerificationMaterial.X509CertificateChain.Certificates {
		der, err := base64.StdEncoding.DecodeString(c.RawBytes)
		if err != nil {
			return nil, fmt.Errorf("decode certificate: %w", err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("parse certificate: %w", err)
		}
		certs = append(certs, cert)
	}
	intermediates := x509.NewCertPool()
	for _, cert := range certs[1:] {
		intermediates.AddCert(cert)
	}
	now := trust.Now
	if now.IsZero() {
		now = time.Now()
	}
	if _, err := certs[0].Verify(x509.VerifyOptions{
		Roots:         trust.Roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return nil, fmt.Errorf("certificate does not chain to the trusted certificate: %w", err)
	}

	env := raw.DsseEnvelope
	if env.PayloadType != inTotoMediaType {
		return nil, fmt.Errorf("unexpected payload type %q", env.PayloadType)
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	pae := dssePAE(env.PayloadType, payload)
	for _, s := range env.Signatures {
		sig, err := base64.StdEncoding.DecodeString(s.Sig)
		if err != nil {
			continue
		}
		if verifySignature(certs[0].PublicKey, pae, sig) {
			return payload, nil
		}
	}
	return nil, errors.New("signature does not verify against the signing certificate")
}

// dssePAE is the DSSE v1 pre-authentication encoding a signature covers.
func dssePAE(payloadType string, payload []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "DSSEv1 %d %s %d ", len(payloadType), payloadType, len(payload))
	b.Write(payload)
	return b.Bytes()
}

// verifySignature checks sig over message with the hash Sigstore pairs with
// the key: SHA-384 for P-384, SHA-512 for P-521, SHA-256 otherwise.
func verifySignature(pub crypto.PublicKey, message, sig []byte) bool {
	switch key := pub.(type) {
	case *ecdsa.PublicKey:
		var digest []byte
		switch key.Curve {
		case elliptic.P384():
			sum := sha512.Sum384(message)
			digest = sum[:]
		case elliptic.P521():
			sum := sha512.Sum512(message)
			digest = sum[:]
		default:
			sum := sha256.Sum256(message)
			digest = sum[:]
		}
		return ecdsa.VerifyASN1(key, digest, sig)
	case *rsa.PublicKey:
		sum := sha256.Sum256(message)
		return rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig) == nil ||
			rsa.VerifyPSS(key, crypto.SHA256, sum[:], sig, nil) == nil
	case ed25519.PublicKey:
		return ed25519.Verify(key, message, sig)
	}
	return false
}

type statement struct {
	PredicateType string `json:"predicateType"`
	Predicate     struct {
		Serialization struct {
			Method        string   `json:"method"`
			HashType      string   `json:"hash_type"`
			AllowSymlinks bool     `json:"allow_symlinks"`
			IgnorePaths   []string `json:"ignore_paths"`
		} `json:"serialization"`
		Resources []struct {
			Name      string `json:"name"`
			Algorithm string `json:"algorithm"`
			Digest    string `json:"digest"`
		} `json:"resources"`
	} `json:"predicate"`
}

// verifyFiles compares dir with the signed statement's file digests.
func verifyFiles(dir string, payload []byte) error {
	var st statement
	if err := json.Unmarshal(payload, &st); err != nil {
		return fmt.Errorf("parse signed statement: %w", err)
	}
	if st.PredicateType != predicateType {
		return fmt.Errorf("unexpected predicate type %q", st.PredicateType)
	}
	ser := st.Predicate.Serialization
	if ser.Method != "files" || ser.HashType != "sha256" {
		return fmt.Errorf("unsupported serialization %s/%s", ser.Method, ser.HashType)
	}
	want := make(map[string]string, len(st.Predicate.Resources))
	for _, r := range st.Predicate.Resources {
		if r.Algorithm != "sha256" {
			return fmt.Errorf("unsupported digest algorithm %q for %s", r.Algorithm, r.Name)
		}
		want[r.Name] = r.Digest
	}
	ignored := func(rel string) bool {
		if rel == FileName {
			return true
		}
		for _, p := range ser.IgnorePaths {
			p = strings.Trim(filepath.ToSlash(p), "/")
			if rel == p || strings.HasPrefix(rel, p+"/") {
				return true
			}
		}
		return false
	}

	got := make(map[string]string)
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if ignored(rel) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink, which a signed Skill may not contain", rel)
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		got[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		return err
	}

	for _, name := range slices.Sorted(maps.Keys(got)) {
		digest, ok := want[name]
		if !ok {
			return fmt.Errorf("%s was not signed", name)
		}
		if digest != got[name] {
			return fmt.Errorf("%s changed after signing", name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(want)) {
		if _, ok := got[name]; !ok {
			return fmt.Errorf("%s is signed but missing", name)
		}
	}
	return nil
}
