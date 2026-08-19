package attestation

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
)

// androidAttestationOID identifies the key attestation extension Android embeds
// in the leaf certificate of the attestation chain.
var androidAttestationOID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 1, 17}

// KeyDescription authorization-list tags used here. The full list is much
// larger; these are the fields that carry security meaning for capture keys.
const (
	tagOrigin                   = 702
	tagRootOfTrust              = 704
	tagAttestationApplicationID = 709
)

// SecurityLevel values from the attestation schema.
const (
	securityLevelSoftware  = 0
	securityLevelTEE       = 1
	securityLevelStrongBox = 2
)

// originGenerated (KM_ORIGIN_GENERATED) means the key pair was created inside
// the secure element and its private half has never existed outside it. Any
// other origin means the key was imported, which defeats the entire point.
const originGenerated = 0

// VerifiedBootState values.
const (
	bootVerified   = 0
	bootSelfSigned = 1
	bootUnverified = 2
	bootFailed     = 3
)

var bootStateNames = map[int]string{
	bootVerified:   "verified",
	bootSelfSigned: "self-signed",
	bootUnverified: "unverified",
	bootFailed:     "failed",
}

var securityLevelNames = map[int]string{
	securityLevelSoftware:  "software",
	securityLevelTEE:       "trusted-environment",
	securityLevelStrongBox: "strongbox",
}

// AndroidConfig is the policy an Android attestation must satisfy.
type AndroidConfig struct {
	// Roots holds the Google hardware attestation roots. Required: without a
	// pinned root, a self-signed chain would verify and the check would be
	// decorative.
	Roots *x509.CertPool
	// AllowedPackages lists the Android application IDs permitted to enrol
	// devices. Required — otherwise any app on any genuine device could mint
	// captures against your registry.
	AllowedPackages []string
	// AllowedSignatureDigests holds SHA-256 digests of the APK signing
	// certificates. Required: a package name alone is trivially spoofed by a
	// repackaged app.
	AllowedSignatureDigests [][]byte
	// MinLevel is the weakest acceptable key location. Defaults to TEE.
	MinLevel domain.AttestationLevel
	// AllowUnverifiedBoot accepts unlocked or tampered bootloaders, and
	// attestations carrying no root of trust at all.
	//
	// The flag is phrased as an opt-out so that the zero value enforces. A
	// caller who forgets to set a RequireVerifiedBoot field would silently get
	// the weaker policy, which is the wrong direction for a security gate to
	// fail. Set it only for lab devices on an isolated registry.
	AllowUnverifiedBoot bool
	// Now is injectable for tests and for verifying historical chains.
	Now func() time.Time
}

// AndroidVerifier implements Android Key Attestation verification.
type AndroidVerifier struct {
	cfg AndroidConfig
}

// NewAndroidVerifier validates the policy and builds a verifier. A policy that
// would accept anything is a configuration error, not a permissive default.
func NewAndroidVerifier(cfg AndroidConfig) (*AndroidVerifier, error) {
	if cfg.Roots == nil {
		return nil, fmt.Errorf("android attestation: root certificate pool is required")
	}
	if len(cfg.AllowedPackages) == 0 {
		return nil, fmt.Errorf("android attestation: at least one allowed package is required")
	}
	if len(cfg.AllowedSignatureDigests) == 0 {
		return nil, fmt.Errorf("android attestation: at least one allowed signature digest is required")
	}
	if cfg.MinLevel == "" {
		cfg.MinLevel = domain.AttestationTEE
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &AndroidVerifier{cfg: cfg}, nil
}

// Verify walks the attestation chain and applies the configured policy.
func (v *AndroidVerifier) Verify(_ context.Context, req domain.AttestationRequest) (*domain.AttestationEvidence, error) {
	leaf, intermediates, err := parseChain(req.CertChain)
	if err != nil {
		return nil, err
	}

	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:         v.cfg.Roots,
		Intermediates: intermediates,
		CurrentTime:   v.cfg.Now(),
		// Attestation certificates carry no serverAuth/clientAuth EKU; the
		// trust decision rests on chaining to a pinned Google root.
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return nil, reject("certificate chain does not verify: %v", err)
	}

	kd, err := extractKeyDescription(leaf)
	if err != nil {
		return nil, err
	}

	if !bytes.Equal(kd.challenge, req.Challenge) {
		// The attestation is for a different challenge: either a replay of an
		// older capture or a chain lifted from another server.
		return nil, reject("attestation challenge does not match the issued nonce")
	}

	level, err := v.checkSecurityLevel(kd)
	if err != nil {
		return nil, err
	}

	if err := checkOrigin(kd); err != nil {
		return nil, err
	}

	bootState, err := v.checkRootOfTrust(kd)
	if err != nil {
		return nil, err
	}

	pkg, err := v.checkApplication(kd)
	if err != nil {
		return nil, err
	}

	publicKeyDER, err := encodeCaptureKey(leaf.PublicKey)
	if err != nil {
		return nil, err
	}

	return &domain.AttestationEvidence{
		PublicKeyDER:      publicKeyDER,
		Level:             level,
		PackageName:       pkg,
		VerifiedBootState: bootState,
		SecurityLevel:     securityLevelNames[kd.keySecurityLevel],
	}, nil
}

// encodeCaptureKey rejects any attested key a capture signature could not be
// verified against, then encodes it.
//
// Android Keystore will happily attest an RSA key. Accepting one here would
// enrol the device successfully and then fail every single capture, which is a
// far worse failure than refusing the enrolment outright: the integrator would
// see a working enrolment and an inexplicably broken capture path.
func encodeCaptureKey(pub any) ([]byte, error) {
	ecPub, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, reject("attested key is %T, want an ECDSA P-256 key", pub)
	}
	if ecPub.Curve != elliptic.P256() {
		return nil, reject("attested key uses curve %s, want P-256", ecPub.Curve.Params().Name)
	}
	der, err := x509.MarshalPKIXPublicKey(ecPub)
	if err != nil {
		return nil, fmt.Errorf("android attestation: encoding attested key: %w", err)
	}
	return der, nil
}

// parseChain decodes the DER chain, leaf first.
func parseChain(chain [][]byte) (*x509.Certificate, *x509.CertPool, error) {
	if len(chain) == 0 {
		return nil, nil, reject("empty attestation certificate chain")
	}
	certs := make([]*x509.Certificate, 0, len(chain))
	for i, der := range chain {
		c, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, nil, reject("certificate %d is not valid DER: %v", i, err)
		}
		certs = append(certs, c)
	}
	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	return certs[0], intermediates, nil
}

// keyDescription is the decoded attestation extension.
type keyDescription struct {
	version              int
	attestSecurityLevel  int
	keySecurityLevel     int
	challenge            []byte
	softwareEnforced     []element
	teeEnforced          []element
	rootOfTrustAvailable bool
}

// extractKeyDescription pulls and decodes the attestation extension from the
// leaf certificate.
func extractKeyDescription(leaf *x509.Certificate) (*keyDescription, error) {
	for _, ext := range leaf.Extensions {
		if ext.Id.Equal(androidAttestationOID) {
			return parseKeyDescription(ext.Value)
		}
	}
	return nil, reject("leaf certificate carries no key attestation extension")
}

// parseKeyDescription decodes the KeyDescription SEQUENCE.
//
//	KeyDescription ::= SEQUENCE {
//	    attestationVersion         INTEGER,
//	    attestationSecurityLevel   SecurityLevel,
//	    keymasterVersion           INTEGER,
//	    keymasterSecurityLevel     SecurityLevel,
//	    attestationChallenge       OCTET_STRING,
//	    uniqueId                   OCTET_STRING,
//	    softwareEnforced           AuthorizationList,
//	    teeEnforced                AuthorizationList,
//	}
func parseKeyDescription(der []byte) (*keyDescription, error) {
	outer, _, err := parseElement(der)
	if err != nil {
		return nil, reject("attestation extension is not valid DER: %v", err)
	}
	fields, err := parseElements(outer.Bytes)
	if err != nil {
		return nil, reject("attestation extension body is malformed: %v", err)
	}
	if len(fields) < 8 {
		return nil, reject("attestation extension has %d fields, want at least 8", len(fields))
	}

	version, err := asInt(fields[0])
	if err != nil {
		return nil, reject("attestationVersion: %v", err)
	}
	attestLevel, err := asInt(fields[1])
	if err != nil {
		return nil, reject("attestationSecurityLevel: %v", err)
	}
	keyLevel, err := asInt(fields[3])
	if err != nil {
		return nil, reject("keymasterSecurityLevel: %v", err)
	}
	if fields[4].Tag != asn1.TagOctetString {
		return nil, reject("attestationChallenge is not an octet string")
	}

	software, err := parseElements(fields[6].Bytes)
	if err != nil {
		return nil, reject("softwareEnforced list is malformed: %v", err)
	}
	tee, err := parseElements(fields[7].Bytes)
	if err != nil {
		return nil, reject("teeEnforced list is malformed: %v", err)
	}

	return &keyDescription{
		version:             version,
		attestSecurityLevel: attestLevel,
		keySecurityLevel:    keyLevel,
		challenge:           fields[4].Bytes,
		softwareEnforced:    software,
		teeEnforced:         tee,
	}, nil
}

// checkSecurityLevel requires both the attestation and the key itself to be
// hardware-backed at the configured minimum.
func (v *AndroidVerifier) checkSecurityLevel(kd *keyDescription) (domain.AttestationLevel, error) {
	attest := mapSecurityLevel(kd.attestSecurityLevel)
	key := mapSecurityLevel(kd.keySecurityLevel)

	// Report the weaker of the two: a StrongBox attestation of a TEE key is
	// only as strong as the key's own location.
	level := key
	if attest.AtLeast(key) == false {
		level = attest
	}

	if !level.AtLeast(v.cfg.MinLevel) {
		return "", reject("key security level %q is below the required %q", level, v.cfg.MinLevel)
	}
	return level, nil
}

func mapSecurityLevel(v int) domain.AttestationLevel {
	switch v {
	case securityLevelStrongBox:
		return domain.AttestationStrongBox
	case securityLevelTEE:
		return domain.AttestationTEE
	default:
		return domain.AttestationSoftware
	}
}

// checkOrigin requires the key to have been generated inside the secure
// element rather than imported into it.
func checkOrigin(kd *keyDescription) error {
	origin, ok, err := explicitInt(kd.teeEnforced, tagOrigin)
	if err != nil {
		return reject("origin: %v", err)
	}
	if !ok {
		return reject("attestation does not state the key origin")
	}
	if origin != originGenerated {
		return reject("key was imported (origin %d), not generated in secure hardware", origin)
	}
	return nil
}

// checkRootOfTrust enforces bootloader integrity.
//
//	RootOfTrust ::= SEQUENCE {
//	    verifiedBootKey    OCTET_STRING,
//	    deviceLocked       BOOLEAN,
//	    verifiedBootState  VerifiedBootState,
//	    verifiedBootHash   OCTET_STRING,   -- attestation v3+
//	}
func (v *AndroidVerifier) checkRootOfTrust(kd *keyDescription) (string, error) {
	rot, ok := contextTag(kd.teeEnforced, tagRootOfTrust)
	if !ok {
		if !v.cfg.AllowUnverifiedBoot {
			return "", reject("attestation carries no root of trust")
		}
		return "unknown", nil
	}

	inner, err := explicitInner(rot)
	if err != nil {
		return "", reject("root of trust: %v", err)
	}
	fields, err := parseElements(inner.Bytes)
	if err != nil {
		return "", reject("root of trust is malformed: %v", err)
	}
	if len(fields) < 3 {
		return "", reject("root of trust has %d fields, want at least 3", len(fields))
	}

	locked, err := asBool(fields[1])
	if err != nil {
		return "", reject("deviceLocked: %v", err)
	}
	state, err := asInt(fields[2])
	if err != nil {
		return "", reject("verifiedBootState: %v", err)
	}

	name, known := bootStateNames[state]
	if !known {
		name = fmt.Sprintf("unknown(%d)", state)
	}

	if !v.cfg.AllowUnverifiedBoot {
		if !locked {
			return "", reject("device bootloader is unlocked")
		}
		if state != bootVerified {
			return "", reject("verified boot state is %q, want %q", name, bootStateNames[bootVerified])
		}
	}
	return name, nil
}

// checkApplication binds the attestation to a known app build.
//
//	AttestationApplicationId ::= SEQUENCE {
//	    packageInfos      SET OF AttestationPackageInfo,
//	    signatureDigests  SET OF OCTET_STRING,
//	}
//	AttestationPackageInfo ::= SEQUENCE {
//	    packageName  OCTET_STRING,
//	    version      INTEGER,
//	}
//
// The field lives in softwareEnforced because the Android framework, not the
// secure element, populates it. That is fine: it is the signature digest that
// carries the weight, and forging it requires the app signing key.
func (v *AndroidVerifier) checkApplication(kd *keyDescription) (string, error) {
	raw, ok, err := explicitOctets(kd.softwareEnforced, tagAttestationApplicationID)
	if err != nil {
		return "", reject("attestationApplicationId: %v", err)
	}
	if !ok {
		// Fall back to teeEnforced: some KeyMint implementations place it there.
		raw, ok, err = explicitOctets(kd.teeEnforced, tagAttestationApplicationID)
		if err != nil {
			return "", reject("attestationApplicationId: %v", err)
		}
	}
	if !ok {
		return "", reject("attestation does not identify the calling application")
	}

	outer, _, err := parseElement(raw)
	if err != nil {
		return "", reject("attestationApplicationId is not valid DER: %v", err)
	}
	parts, err := parseElements(outer.Bytes)
	if err != nil || len(parts) < 2 {
		return "", reject("attestationApplicationId is malformed")
	}

	pkg, err := matchPackage(parts[0], v.cfg.AllowedPackages)
	if err != nil {
		return "", err
	}
	if err := matchSignatureDigest(parts[1], v.cfg.AllowedSignatureDigests); err != nil {
		return "", err
	}
	return pkg, nil
}

// matchPackage requires at least one declared package to be on the allowlist.
func matchPackage(packageInfos element, allowed []string) (string, error) {
	infos, err := parseElements(packageInfos.Bytes)
	if err != nil {
		return "", reject("packageInfos is malformed: %v", err)
	}
	var seen []string
	for _, info := range infos {
		fields, err := parseElements(info.Bytes)
		if err != nil || len(fields) < 1 {
			continue
		}
		name := string(fields[0].Bytes)
		seen = append(seen, name)
		for _, a := range allowed {
			if name == a {
				return name, nil
			}
		}
	}
	return "", reject("application %v is not an enrolled capture app", seen)
}

// matchSignatureDigest requires the APK to be signed by a known key.
func matchSignatureDigest(digests element, allowed [][]byte) error {
	items, err := parseElements(digests.Bytes)
	if err != nil {
		return reject("signatureDigests is malformed: %v", err)
	}
	for _, item := range items {
		for _, a := range allowed {
			if bytes.Equal(item.Bytes, a) {
				return nil
			}
		}
	}
	return reject("application is signed by an unrecognised key")
}
