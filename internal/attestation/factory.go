package attestation

import (
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/waizbart/aletheia-api/internal/domain"
)

// NewRegistryFromEnv builds the platform verifiers from configuration.
//
// Android is the only platform wired today; iOS App Attest lands with the iOS
// SDK, and until then an iOS capture is rejected as an unsupported platform
// rather than being silently accepted on a weaker check.
//
// Environment:
//
//	ANDROID_ATTESTATION_ROOTS      path to a PEM bundle of Google hardware
//	                               attestation roots (required)
//	ANDROID_ALLOWED_PACKAGES       comma-separated application IDs (required)
//	ANDROID_SIGNATURE_DIGESTS      comma-separated hex SHA-256 digests of the
//	                               APK signing certificates (required)
//	ANDROID_MIN_ATTESTATION_LEVEL  "tee" (default) or "strongbox"
//	ANDROID_REQUIRE_VERIFIED_BOOT  "true" (default) or "false"
func NewRegistryFromEnv() (*Registry, error) {
	rootsPath := os.Getenv("ANDROID_ATTESTATION_ROOTS")
	packages := splitList(os.Getenv("ANDROID_ALLOWED_PACKAGES"))
	digestsHex := splitList(os.Getenv("ANDROID_SIGNATURE_DIGESTS"))

	// With nothing configured, start with no verifiers at all rather than with
	// a permissive one. The API still serves verification, and device enrolment
	// reports the platform as unsupported — which is true, and is the only safe
	// reading of "attestation is not set up yet".
	if rootsPath == "" && len(packages) == 0 && len(digestsHex) == 0 {
		return NewRegistry(nil), nil
	}

	if rootsPath == "" || len(packages) == 0 || len(digestsHex) == 0 {
		return nil, fmt.Errorf("attestation: ANDROID_ATTESTATION_ROOTS, ANDROID_ALLOWED_PACKAGES and ANDROID_SIGNATURE_DIGESTS must be set together")
	}

	roots, err := LoadRootsPEM(rootsPath)
	if err != nil {
		return nil, err
	}

	digests := make([][]byte, 0, len(digestsHex))
	for _, h := range digestsHex {
		d, err := hex.DecodeString(h)
		if err != nil {
			return nil, fmt.Errorf("attestation: signature digest %q is not hex: %w", h, err)
		}
		digests = append(digests, d)
	}

	minLevel := domain.AttestationLevel(strings.ToLower(os.Getenv("ANDROID_MIN_ATTESTATION_LEVEL")))
	switch minLevel {
	case "":
		minLevel = domain.AttestationTEE
	case domain.AttestationTEE, domain.AttestationStrongBox:
	default:
		return nil, fmt.Errorf("attestation: ANDROID_MIN_ATTESTATION_LEVEL must be tee or strongbox, got %q", minLevel)
	}

	// The env var keeps its "require" phrasing because that is what an
	// operator reads in a config file; only the struct field is an opt-out.
	allowUnverifiedBoot := false
	if v := os.Getenv("ANDROID_REQUIRE_VERIFIED_BOOT"); v != "" {
		allowUnverifiedBoot = strings.EqualFold(v, "false") || v == "0"
	}

	android, err := NewAndroidVerifier(AndroidConfig{
		Roots:                   roots,
		AllowedPackages:         packages,
		AllowedSignatureDigests: digests,
		MinLevel:                minLevel,
		AllowUnverifiedBoot:     allowUnverifiedBoot,
	})
	if err != nil {
		return nil, err
	}

	return NewRegistry(map[domain.Platform]Verifier{
		domain.PlatformAndroid: android,
	}), nil
}

// LoadRootsPEM reads a PEM bundle into a certificate pool.
func LoadRootsPEM(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("attestation: reading roots %s: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("attestation: %s contains no usable certificates", path)
	}
	return pool, nil
}

func splitList(v string) []string {
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
