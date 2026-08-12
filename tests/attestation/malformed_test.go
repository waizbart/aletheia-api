package attestation_test

import (
	"context"
	"encoding/asn1"
	"strings"
	"testing"

	"github.com/waizbart/aletheia-api/internal/attestation"
	"github.com/waizbart/aletheia-api/internal/domain"
)

// A hostile client controls every byte of the attestation extension. These
// cases feed deliberately broken DER through the same code path a genuine
// attestation takes, so a parser bug surfaces as a clean rejection rather than
// a panic or an accidental accept.
func TestAndroidVerifier_MalformedExtension(t *testing.T) {
	validAppID := derSeq(
		derSet(derSeq(derOctets([]byte(testPackage)), derInt(1))),
		derSet(derOctets(testSignatureDigest)),
	)
	validRoT := derSeq(
		derOctets([]byte("k")),
		derBool(true),
		derEnum(0),
	)

	// keyDesc assembles a KeyDescription from explicit software/tee lists so a
	// case can corrupt exactly one part.
	keyDesc := func(software, tee []byte) []byte {
		return derSeq(
			derInt(4),
			derEnum(1),
			derInt(4),
			derEnum(1),
			derOctets(testChallenge),
			derOctets(nil),
			software,
			tee,
		)
	}
	goodTee := derSeq(
		derExplicit(tagOrigin, derInt(0)),
		derExplicit(tagRootOfTrust, validRoT),
	)
	goodSoftware := derSeq(derExplicit(tagAttestationApplicationID, derOctets(validAppID)))

	tests := []struct {
		name         string
		raw          []byte
		wantContains string
	}{
		{
			name:         "extension is not DER at all",
			raw:          []byte{0xff, 0xff, 0xff},
			wantContains: "not valid DER",
		},
		{
			// An element inside the SEQUENCE declares nine content bytes but
			// supplies one, so the body fails to walk.
			name:         "key description body is truncated mid-element",
			raw:          tlv(0, true, asn1.TagSequence, []byte{0x02, 0x09, 0x01}),
			wantContains: "body is malformed",
		},
		{
			name:         "key description has too few fields",
			raw:          derSeq(derInt(4), derEnum(1)),
			wantContains: "want at least 8",
		},
		{
			name: "attestation version is oversized",
			raw: derSeq(
				tlv(0, false, asn1.TagInteger, []byte{1, 2, 3, 4, 5}),
				derEnum(1), derInt(4), derEnum(1),
				derOctets(testChallenge), derOctets(nil), goodSoftware, goodTee,
			),
			wantContains: "attestationVersion",
		},
		{
			name: "attestation security level is oversized",
			raw: derSeq(
				derInt(4),
				tlv(0, false, asn1.TagEnum, []byte{1, 2, 3, 4, 5}),
				derInt(4), derEnum(1),
				derOctets(testChallenge), derOctets(nil), goodSoftware, goodTee,
			),
			wantContains: "attestationSecurityLevel",
		},
		{
			name: "keymaster security level is oversized",
			raw: derSeq(
				derInt(4), derEnum(1), derInt(4),
				tlv(0, false, asn1.TagEnum, []byte{1, 2, 3, 4, 5}),
				derOctets(testChallenge), derOctets(nil), goodSoftware, goodTee,
			),
			wantContains: "keymasterSecurityLevel",
		},
		{
			// High bit set: read as a negative origin, which is not
			// KM_ORIGIN_GENERATED and must be refused.
			name: "origin is negative",
			raw: keyDesc(goodSoftware, derSeq(
				derExplicit(tagOrigin, tlv(0, false, asn1.TagInteger, []byte{0x80})),
				derExplicit(tagRootOfTrust, validRoT),
			)),
			wantContains: "imported",
		},
		{
			name: "origin explicit tag wraps malformed DER",
			raw: keyDesc(goodSoftware, derSeq(
				derExplicit(tagOrigin, []byte{0x02, 0x09, 0x01}),
				derExplicit(tagRootOfTrust, validRoT),
			)),
			wantContains: "origin",
		},
		{
			name: "application id explicit tag wraps malformed DER",
			raw: keyDesc(
				derSeq(derExplicit(tagAttestationApplicationID, []byte{0x04, 0x09, 0x01})),
				goodTee,
			),
			wantContains: "attestationApplicationId",
		},
		{
			name: "application id in the tee list wraps malformed DER",
			raw: keyDesc(
				derSeq(),
				derSeq(
					derExplicit(tagOrigin, derInt(0)),
					derExplicit(tagRootOfTrust, validRoT),
					derExplicit(tagAttestationApplicationID, []byte{0x04, 0x09, 0x01}),
				),
			),
			wantContains: "attestationApplicationId",
		},
		{
			name: "root of trust explicit tag wraps malformed DER",
			raw: keyDesc(goodSoftware, derSeq(
				derExplicit(tagOrigin, derInt(0)),
				derExplicit(tagRootOfTrust, []byte{0x30, 0x09, 0x01}),
			)),
			wantContains: "root of trust",
		},
		{
			name: "attestation challenge is not an octet string",
			raw: derSeq(
				derInt(4), derEnum(1), derInt(4), derEnum(1),
				derInt(7), // challenge slot holds an INTEGER
				derOctets(nil), goodSoftware, goodTee,
			),
			wantContains: "not an octet string",
		},
		{
			name: "software list is not a sequence body",
			raw: derSeq(
				derInt(4), derEnum(1), derInt(4), derEnum(1),
				derOctets(testChallenge), derOctets(nil),
				tlv(0, true, asn1.TagSequence, []byte{0x02, 0x09, 0x01}), // bad inner
				goodTee,
			),
			wantContains: "softwareEnforced list is malformed",
		},
		{
			name: "tee list is not a sequence body",
			raw: derSeq(
				derInt(4), derEnum(1), derInt(4), derEnum(1),
				derOctets(testChallenge), derOctets(nil),
				goodSoftware,
				tlv(0, true, asn1.TagSequence, []byte{0x02, 0x09, 0x01}),
			),
			wantContains: "teeEnforced list is malformed",
		},
		{
			name: "origin integer is oversized",
			raw: keyDesc(goodSoftware, derSeq(
				derExplicit(tagOrigin, tlv(0, false, asn1.TagInteger, []byte{1, 2, 3, 4, 5})),
				derExplicit(tagRootOfTrust, validRoT),
			)),
			wantContains: "origin",
		},
		{
			name: "origin has trailing bytes inside its explicit tag",
			raw: keyDesc(goodSoftware, derSeq(
				derExplicit(tagOrigin, append(derInt(0), derInt(1)...)),
				derExplicit(tagRootOfTrust, validRoT),
			)),
			wantContains: "trailing bytes",
		},
		{
			name: "root of trust has too few fields",
			raw: keyDesc(goodSoftware, derSeq(
				derExplicit(tagOrigin, derInt(0)),
				derExplicit(tagRootOfTrust, derSeq(derOctets([]byte("k")), derBool(true))),
			)),
			wantContains: "want at least 3",
		},
		{
			name: "device locked flag is not a single byte",
			raw: keyDesc(goodSoftware, derSeq(
				derExplicit(tagOrigin, derInt(0)),
				derExplicit(tagRootOfTrust, derSeq(
					derOctets([]byte("k")),
					tlv(0, false, asn1.TagBoolean, []byte{0x01, 0x02}),
					derEnum(0),
				)),
			)),
			wantContains: "deviceLocked",
		},
		{
			name: "boot state enum is empty",
			raw: keyDesc(goodSoftware, derSeq(
				derExplicit(tagOrigin, derInt(0)),
				derExplicit(tagRootOfTrust, derSeq(
					derOctets([]byte("k")),
					derBool(true),
					tlv(0, false, asn1.TagEnum, nil),
				)),
			)),
			wantContains: "verifiedBootState",
		},
		{
			name: "root of trust body is malformed",
			raw: keyDesc(goodSoftware, derSeq(
				derExplicit(tagOrigin, derInt(0)),
				derExplicit(tagRootOfTrust, tlv(0, true, asn1.TagSequence, []byte{0x04, 0x09, 0x01})),
			)),
			wantContains: "root of trust is malformed",
		},
		{
			name: "application id field is not an octet string",
			raw: keyDesc(
				derSeq(derExplicit(tagAttestationApplicationID, derInt(1))),
				goodTee,
			),
			wantContains: "not an octet string",
		},
		{
			name: "application id contents are not DER",
			raw: keyDesc(
				derSeq(derExplicit(tagAttestationApplicationID, derOctets([]byte{0xff, 0xff}))),
				goodTee,
			),
			wantContains: "not valid DER",
		},
		{
			name: "application id has only one part",
			raw: keyDesc(
				derSeq(derExplicit(tagAttestationApplicationID, derOctets(
					derSeq(derSet(derSeq(derOctets([]byte(testPackage)), derInt(1)))),
				))),
				goodTee,
			),
			wantContains: "malformed",
		},
		{
			name: "package infos body is malformed",
			raw: keyDesc(
				derSeq(derExplicit(tagAttestationApplicationID, derOctets(derSeq(
					tlv(0, true, asn1.TagSet, []byte{0x30, 0x09, 0x01}),
					derSet(derOctets(testSignatureDigest)),
				)))),
				goodTee,
			),
			wantContains: "packageInfos is malformed",
		},
		{
			name: "signature digests body is malformed",
			raw: keyDesc(
				derSeq(derExplicit(tagAttestationApplicationID, derOctets(derSeq(
					derSet(derSeq(derOctets([]byte(testPackage)), derInt(1))),
					tlv(0, true, asn1.TagSet, []byte{0x04, 0x09, 0x01}),
				)))),
				goodTee,
			),
			wantContains: "signatureDigests is malformed",
		},
		{
			name: "package info entries that do not parse are skipped",
			raw: keyDesc(
				derSeq(derExplicit(tagAttestationApplicationID, derOctets(derSeq(
					derSet(
						tlv(0, true, asn1.TagSequence, []byte{0x04, 0x09, 0x01}),
						derSeq(derOctets([]byte("com.other.app")), derInt(1)),
					),
					derSet(derOctets(testSignatureDigest)),
				)))),
				goodTee,
			),
			wantContains: "not an enrolled capture app",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kd := validKeyDesc(testChallenge)
			kd.raw = tt.raw
			fx := buildChain(t, kd, false)

			v := newVerifier(t, fx.rootPool, nil)
			_, err := v.Verify(context.Background(), domain.AttestationRequest{
				Challenge: testChallenge,
				CertChain: fx.chain,
			})
			if err == nil {
				t.Fatal("expected rejection, got nil error")
			}
			if !strings.Contains(err.Error(), tt.wantContains) {
				t.Errorf("error %q does not mention %q", err.Error(), tt.wantContains)
			}
		})
	}
}

// TestAndroidVerifier_ReportsWeakerOfTwoLevels pins the conservative reading of
// a mixed attestation: a StrongBox attestation of a TEE-resident key is only as
// strong as where the key actually lives.
func TestAndroidVerifier_ReportsWeakerOfTwoLevels(t *testing.T) {
	kd := validKeyDesc(testChallenge)
	kd.attestLevel = 1 // TEE attestation
	kd.keyLevel = 2    // of a StrongBox key
	fx := buildChain(t, kd, false)

	v := newVerifier(t, fx.rootPool, func(c *attestation.AndroidConfig) { c.MinLevel = domain.AttestationStrongBox })

	if _, err := v.Verify(context.Background(), domain.AttestationRequest{
		Challenge: testChallenge,
		CertChain: fx.chain,
	}); err == nil {
		t.Fatal("a TEE attestation must not satisfy a StrongBox policy")
	}
}

func TestAndroidVerifier_UnknownBootStateIsNamed(t *testing.T) {
	kd := validKeyDesc(testChallenge)
	kd.bootState = 9
	fx := buildChain(t, kd, false)

	v := newVerifier(t, fx.rootPool, nil)
	_, err := v.Verify(context.Background(), domain.AttestationRequest{
		Challenge: testChallenge,
		CertChain: fx.chain,
	})
	if err == nil {
		t.Fatal("expected rejection")
	}
	if !strings.Contains(err.Error(), "unknown(9)") {
		t.Errorf("error %q should name the unrecognised state", err.Error())
	}
}

// TestAndroidVerifier_RootOfTrustOptionalWhenBootNotRequired covers the lab
// configuration: with verified boot disabled, an attestation carrying no root
// of trust is accepted and reported as unknown rather than rejected.
func TestAndroidVerifier_RootOfTrustOptionalWhenBootNotRequired(t *testing.T) {
	kd := validKeyDesc(testChallenge)
	kd.omitRootOfTrust = true
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
	if got.VerifiedBootState != "unknown" {
		t.Errorf("VerifiedBootState = %q, want unknown", got.VerifiedBootState)
	}
}
