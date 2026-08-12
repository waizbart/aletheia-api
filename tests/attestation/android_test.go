package attestation_test

import (
	"context"
	"crypto/x509"
	"errors"
	"strings"
	"testing"

	"github.com/waizbart/aletheia-api/internal/attestation"
	"github.com/waizbart/aletheia-api/internal/domain"
)

var testChallenge = []byte("f0e1d2c3b4a5968778695a4b3c2d1e0ff0e1d2c3b4a5968778695a4b3c2d1e0f")

func newVerifier(t *testing.T, roots *x509.CertPool, mutate func(*attestation.AndroidConfig)) *attestation.AndroidVerifier {
	t.Helper()
	cfg := attestation.AndroidConfig{
		Roots:                   roots,
		AllowedPackages:         []string{testPackage},
		AllowedSignatureDigests: [][]byte{testSignatureDigest},
		MinLevel:                domain.AttestationTEE,
		RequireVerifiedBoot:     true,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	v, err := attestation.NewAndroidVerifier(cfg)
	if err != nil {
		t.Fatalf("NewAndroidVerifier: %v", err)
	}
	return v
}

func TestAndroidVerifier_AcceptsValidAttestation(t *testing.T) {
	fx := buildChain(t, validKeyDesc(testChallenge), false)
	v := newVerifier(t, fx.rootPool, nil)

	got, err := v.Verify(context.Background(), domain.AttestationRequest{
		Platform:  domain.PlatformAndroid,
		Challenge: testChallenge,
		CertChain: fx.chain,
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if string(got.PublicKeyDER) != string(fx.leafPubKey) {
		t.Error("returned key is not the attested leaf key")
	}
	if got.Level != domain.AttestationTEE {
		t.Errorf("Level = %q, want %q", got.Level, domain.AttestationTEE)
	}
	if got.PackageName != testPackage {
		t.Errorf("PackageName = %q, want %q", got.PackageName, testPackage)
	}
	if got.VerifiedBootState != "verified" {
		t.Errorf("VerifiedBootState = %q, want verified", got.VerifiedBootState)
	}
}

func TestAndroidVerifier_ReportsStrongBox(t *testing.T) {
	kd := validKeyDesc(testChallenge)
	kd.attestLevel, kd.keyLevel = 2, 2
	fx := buildChain(t, kd, false)

	v := newVerifier(t, fx.rootPool, func(c *attestation.AndroidConfig) {
		c.MinLevel = domain.AttestationStrongBox
	})

	got, err := v.Verify(context.Background(), domain.AttestationRequest{
		Challenge: testChallenge,
		CertChain: fx.chain,
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Level != domain.AttestationStrongBox {
		t.Errorf("Level = %q, want strongbox", got.Level)
	}
}

// TestAndroidVerifier_Rejects covers every policy gate. Each case starts from a
// valid attestation and breaks exactly one property, so a passing case proves
// that gate — and only that gate — is what rejected it.
func TestAndroidVerifier_Rejects(t *testing.T) {
	tests := []struct {
		name           string
		kd             func(*keyDescOptions)
		cfg            func(*attestation.AndroidConfig)
		challenge      []byte
		omitExtension  bool
		useForeignRoot bool
		emptyChain     bool
		wantContains   string
	}{
		{
			name:         "challenge mismatch",
			challenge:    []byte("a different challenge entirely, 64 chars padded out......"),
			wantContains: "challenge does not match",
		},
		{
			name:         "software-only key",
			kd:           func(o *keyDescOptions) { o.attestLevel, o.keyLevel = 0, 0 },
			wantContains: "below the required",
		},
		{
			name:         "TEE attestation of a software key",
			kd:           func(o *keyDescOptions) { o.keyLevel = 0 },
			wantContains: "below the required",
		},
		{
			name:         "imported key",
			kd:           func(o *keyDescOptions) { o.origin = 2 },
			wantContains: "imported",
		},
		{
			name:         "origin absent",
			kd:           func(o *keyDescOptions) { o.omitOrigin = true },
			wantContains: "does not state the key origin",
		},
		{
			name:         "unlocked bootloader",
			kd:           func(o *keyDescOptions) { o.deviceLocked = false },
			wantContains: "bootloader is unlocked",
		},
		{
			name:         "unverified boot state",
			kd:           func(o *keyDescOptions) { o.bootState = 2 },
			wantContains: "verified boot state",
		},
		{
			name:         "no root of trust",
			kd:           func(o *keyDescOptions) { o.omitRootOfTrust = true },
			wantContains: "no root of trust",
		},
		{
			name:         "unknown package",
			kd:           func(o *keyDescOptions) { o.packageName = "com.attacker.repackaged" },
			wantContains: "not an enrolled capture app",
		},
		{
			name:         "wrong signing key",
			kd:           func(o *keyDescOptions) { o.signatureDigest = []byte("ffffffffffffffffffffffffffffffff") },
			wantContains: "unrecognised key",
		},
		{
			name:         "application id absent",
			kd:           func(o *keyDescOptions) { o.omitApplicationID = true },
			wantContains: "does not identify the calling application",
		},
		{
			name:         "truncated key description",
			kd:           func(o *keyDescOptions) { o.truncatedFields = true },
			wantContains: "want at least 8",
		},
		{
			name:          "no attestation extension",
			omitExtension: true,
			wantContains:  "no key attestation extension",
		},
		{
			name:           "chain does not reach a pinned root",
			useForeignRoot: true,
			wantContains:   "chain does not verify",
		},
		{
			name:         "empty chain",
			emptyChain:   true,
			wantContains: "empty attestation certificate chain",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kd := validKeyDesc(testChallenge)
			if tt.kd != nil {
				tt.kd(&kd)
			}
			fx := buildChain(t, kd, tt.omitExtension)

			roots := fx.rootPool
			if tt.useForeignRoot {
				// A root from an unrelated chain: structurally fine, but not
				// the one we pin.
				roots = buildChain(t, validKeyDesc(testChallenge), false).rootPool
			}

			chain := fx.chain
			if tt.emptyChain {
				chain = nil
			}

			challenge := testChallenge
			if tt.challenge != nil {
				challenge = tt.challenge
			}

			v := newVerifier(t, roots, tt.cfg)
			_, err := v.Verify(context.Background(), domain.AttestationRequest{
				Challenge: challenge,
				CertChain: chain,
			})
			if err == nil {
				t.Fatal("expected rejection, got nil error")
			}

			var rejected *attestation.ErrRejected
			if !errors.As(err, &rejected) {
				t.Fatalf("error %v is not an ErrRejected", err)
			}
			if !strings.Contains(err.Error(), tt.wantContains) {
				t.Errorf("error %q does not mention %q", err.Error(), tt.wantContains)
			}
		})
	}
}

func TestAndroidVerifier_AcceptsApplicationIDInTEEList(t *testing.T) {
	kd := validKeyDesc(testChallenge)
	kd.appIDInTEE = true
	fx := buildChain(t, kd, false)

	v := newVerifier(t, fx.rootPool, nil)
	if _, err := v.Verify(context.Background(), domain.AttestationRequest{
		Challenge: testChallenge,
		CertChain: fx.chain,
	}); err != nil {
		t.Fatalf("KeyMint may place the application id in teeEnforced: %v", err)
	}
}

func TestAndroidVerifier_VerifiedBootOptional(t *testing.T) {
	kd := validKeyDesc(testChallenge)
	kd.deviceLocked = false
	kd.bootState = 2
	fx := buildChain(t, kd, false)

	v := newVerifier(t, fx.rootPool, func(c *attestation.AndroidConfig) {
		c.RequireVerifiedBoot = false
	})

	got, err := v.Verify(context.Background(), domain.AttestationRequest{
		Challenge: testChallenge,
		CertChain: fx.chain,
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// The state is still reported, so an operator can see what they accepted.
	if got.VerifiedBootState != "unverified" {
		t.Errorf("VerifiedBootState = %q, want unverified", got.VerifiedBootState)
	}
}

func TestAndroidVerifier_MalformedCertificate(t *testing.T) {
	fx := buildChain(t, validKeyDesc(testChallenge), false)
	v := newVerifier(t, fx.rootPool, nil)

	_, err := v.Verify(context.Background(), domain.AttestationRequest{
		Challenge: testChallenge,
		CertChain: [][]byte{[]byte("not a certificate")},
	})
	if err == nil {
		t.Fatal("expected rejection")
	}
	if !strings.Contains(err.Error(), "not valid DER") {
		t.Errorf("error %q does not mention invalid DER", err.Error())
	}
}

func TestNewAndroidVerifier_RequiresPolicy(t *testing.T) {
	pool := x509.NewCertPool()

	tests := []struct {
		name string
		cfg  attestation.AndroidConfig
		want string
	}{
		{
			name: "roots required",
			cfg:  attestation.AndroidConfig{AllowedPackages: []string{"a"}, AllowedSignatureDigests: [][]byte{{1}}},
			want: "root certificate pool is required",
		},
		{
			name: "packages required",
			cfg:  attestation.AndroidConfig{Roots: pool, AllowedSignatureDigests: [][]byte{{1}}},
			want: "allowed package is required",
		},
		{
			name: "signature digests required",
			cfg:  attestation.AndroidConfig{Roots: pool, AllowedPackages: []string{"a"}},
			want: "signature digest is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := attestation.NewAndroidVerifier(tt.cfg)
			if err == nil {
				t.Fatal("expected a configuration error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", err.Error(), tt.want)
			}
		})
	}
}

func TestNewAndroidVerifier_DefaultsToTEE(t *testing.T) {
	fx := buildChain(t, validKeyDesc(testChallenge), false)
	v, err := attestation.NewAndroidVerifier(attestation.AndroidConfig{
		Roots:                   fx.rootPool,
		AllowedPackages:         []string{testPackage},
		AllowedSignatureDigests: [][]byte{testSignatureDigest},
		RequireVerifiedBoot:     true,
	})
	if err != nil {
		t.Fatal(err)
	}

	kd := validKeyDesc(testChallenge)
	kd.attestLevel, kd.keyLevel = 0, 0
	soft := buildChain(t, kd, false)
	softVerifier, _ := attestation.NewAndroidVerifier(attestation.AndroidConfig{
		Roots:                   soft.rootPool,
		AllowedPackages:         []string{testPackage},
		AllowedSignatureDigests: [][]byte{testSignatureDigest},
		RequireVerifiedBoot:     true,
	})
	if _, err := softVerifier.Verify(context.Background(), domain.AttestationRequest{
		Challenge: testChallenge,
		CertChain: soft.chain,
	}); err == nil {
		t.Error("an unset MinLevel must default to TEE, not to software")
	}

	if _, err := v.Verify(context.Background(), domain.AttestationRequest{
		Challenge: testChallenge,
		CertChain: fx.chain,
	}); err != nil {
		t.Errorf("TEE attestation should pass the default policy: %v", err)
	}
}

func TestRegistry_DispatchAndUnsupported(t *testing.T) {
	fx := buildChain(t, validKeyDesc(testChallenge), false)
	reg := attestation.NewRegistry(map[domain.Platform]attestation.Verifier{
		domain.PlatformAndroid: newVerifier(t, fx.rootPool, nil),
	})

	if _, err := reg.Verify(context.Background(), domain.AttestationRequest{
		Platform:  domain.PlatformAndroid,
		Challenge: testChallenge,
		CertChain: fx.chain,
	}); err != nil {
		t.Fatalf("android should dispatch: %v", err)
	}

	// iOS App Attest lands with the iOS SDK. Until then a capture claiming to
	// be from iOS must be refused, never accepted on a weaker check.
	_, err := reg.Verify(context.Background(), domain.AttestationRequest{
		Platform:  domain.PlatformIOS,
		Challenge: testChallenge,
	})
	if !errors.Is(err, attestation.ErrUnsupportedPlatform) {
		t.Fatalf("error = %v, want ErrUnsupportedPlatform", err)
	}

	if got := reg.Platforms(); len(got) != 1 || got[0] != domain.PlatformAndroid {
		t.Errorf("Platforms() = %v, want [android]", got)
	}
}
