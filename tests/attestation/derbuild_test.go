package attestation_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"testing"
	"time"
)

// This file builds synthetic Android Key Attestation chains. Real chains cannot
// be committed as fixtures — they are device- and challenge-specific, and a
// recorded one expires — so the tests construct chains whose shape matches the
// attestation schema and vary one property at a time.

// --- minimal DER writer -----------------------------------------------------

// tlv encodes one DER element, including the high-tag-number form the
// attestation authorization list needs for tags such as 702 and 709.
func tlv(class int, constructed bool, tag int, content []byte) []byte {
	var identifier []byte
	first := byte(class << 6)
	if constructed {
		first |= 0x20
	}

	if tag < 0x1f {
		identifier = []byte{first | byte(tag)}
	} else {
		identifier = []byte{first | 0x1f}
		// base-128, most significant group first, continuation bit on all but
		// the last octet.
		var groups []byte
		for t := tag; t > 0; t >>= 7 {
			groups = append([]byte{byte(t & 0x7f)}, groups...)
		}
		for i := 0; i < len(groups)-1; i++ {
			groups[i] |= 0x80
		}
		identifier = append(identifier, groups...)
	}

	return append(append(identifier, encodeLength(len(content))...), content...)
}

func encodeLength(n int) []byte {
	if n < 0x80 {
		return []byte{byte(n)}
	}
	var out []byte
	for v := n; v > 0; v >>= 8 {
		out = append([]byte{byte(v & 0xff)}, out...)
	}
	return append([]byte{byte(0x80 | len(out))}, out...)
}

func derSeq(items ...[]byte) []byte { return tlv(0, true, asn1.TagSequence, concat(items...)) }
func derSet(items ...[]byte) []byte { return tlv(0, true, asn1.TagSet, concat(items...)) }
func derOctets(b []byte) []byte     { return tlv(0, false, asn1.TagOctetString, b) }
func derBool(v bool) []byte {
	b := byte(0x00)
	if v {
		b = 0xff
	}
	return tlv(0, false, asn1.TagBoolean, []byte{b})
}

func derInt(v int) []byte  { return tlv(0, false, asn1.TagInteger, intBytes(v)) }
func derEnum(v int) []byte { return tlv(0, false, asn1.TagEnum, intBytes(v)) }

// derExplicit wraps inner in an explicitly tagged context-specific element.
func derExplicit(tag int, inner []byte) []byte { return tlv(2, true, tag, inner) }

func intBytes(v int) []byte {
	if v == 0 {
		return []byte{0}
	}
	b := big.NewInt(int64(v)).Bytes()
	// Prevent a positive value from being read as negative.
	if b[0]&0x80 != 0 {
		b = append([]byte{0}, b...)
	}
	return b
}

func concat(items ...[]byte) []byte {
	var out []byte
	for _, i := range items {
		out = append(out, i...)
	}
	return out
}

// --- attestation extension builder ------------------------------------------

const (
	tagOrigin                   = 702
	tagRootOfTrust              = 704
	tagAttestationApplicationID = 709
)

// keyDescOptions varies one property of an otherwise-valid attestation.
type keyDescOptions struct {
	challenge         []byte
	attestLevel       int
	keyLevel          int
	origin            int
	omitOrigin        bool
	deviceLocked      bool
	bootState         int
	omitRootOfTrust   bool
	packageName       string
	signatureDigest   []byte
	omitApplicationID bool
	appIDInTEE        bool
	truncatedFields   bool

	// raw replaces the whole encoded KeyDescription, so tests can feed
	// deliberately malformed DER through the same chain builder.
	raw []byte
}

func validKeyDesc(challenge []byte) keyDescOptions {
	return keyDescOptions{
		challenge:       challenge,
		attestLevel:     1, // TrustedEnvironment
		keyLevel:        1,
		origin:          0, // KM_ORIGIN_GENERATED
		deviceLocked:    true,
		bootState:       0, // Verified
		packageName:     testPackage,
		signatureDigest: testSignatureDigest,
	}
}

// buildKeyDescription encodes the KeyDescription SEQUENCE.
func buildKeyDescription(o keyDescOptions) []byte {
	if o.raw != nil {
		return o.raw
	}

	var teeItems [][]byte
	if !o.omitOrigin {
		teeItems = append(teeItems, derExplicit(tagOrigin, derInt(o.origin)))
	}
	if !o.omitRootOfTrust {
		rot := derSeq(
			derOctets([]byte("verified-boot-key")),
			derBool(o.deviceLocked),
			derEnum(o.bootState),
			derOctets([]byte("verified-boot-hash")),
		)
		teeItems = append(teeItems, derExplicit(tagRootOfTrust, rot))
	}

	appID := derSeq(
		derSet(derSeq(derOctets([]byte(o.packageName)), derInt(1))),
		derSet(derOctets(o.signatureDigest)),
	)
	appIDField := derExplicit(tagAttestationApplicationID, derOctets(appID))

	var softwareItems [][]byte
	if !o.omitApplicationID {
		if o.appIDInTEE {
			teeItems = append(teeItems, appIDField)
		} else {
			softwareItems = append(softwareItems, appIDField)
		}
	}

	fields := [][]byte{
		derInt(4),              // attestationVersion
		derEnum(o.attestLevel), // attestationSecurityLevel
		derInt(4),              // keymasterVersion
		derEnum(o.keyLevel),    // keymasterSecurityLevel
		derOctets(o.challenge), // attestationChallenge
		derOctets(nil),         // uniqueId
		derSeq(softwareItems...),
		derSeq(teeItems...),
	}
	if o.truncatedFields {
		fields = fields[:5]
	}
	return derSeq(fields...)
}

// --- certificate chain builder ----------------------------------------------

const testPackage = "com.aletheia.camera"

var testSignatureDigest = []byte("0123456789abcdef0123456789abcdef")

var attestationOID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 11129, 2, 1, 17}

type chainFixture struct {
	rootPool   *x509.CertPool
	chain      [][]byte
	leafKey    *ecdsa.PrivateKey
	leafPubKey []byte
}

// buildChain creates a self-signed root and a leaf carrying the attestation
// extension. omitExtension produces a leaf with no attestation at all.
func buildChain(t *testing.T, kd keyDescOptions, omitExtension bool) chainFixture {
	t.Helper()
	return buildChainWithLeafKey(t, kd, omitExtension, nil)
}

// buildChainWithLeafKey is buildChain with control over the attested key type,
// so a chain carrying a key no capture signature could verify against can be
// exercised. A nil leafPub means the usual ECDSA P-256 key.
func buildChainWithLeafKey(t *testing.T, kd keyDescOptions, omitExtension bool, leafPub crypto.PublicKey) chainFixture {
	t.Helper()

	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rootTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Attestation Root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	rootCert, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certifiedPub := crypto.PublicKey(&leafKey.PublicKey)
	if leafPub != nil {
		certifiedPub = leafPub
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "Attested Key"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	if !omitExtension {
		leafTmpl.ExtraExtensions = []pkix.Extension{{
			Id:    attestationOID,
			Value: buildKeyDescription(kd),
		}}
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, rootCert, certifiedPub, rootKey)
	if err != nil {
		t.Fatal(err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(rootCert)

	pub, err := x509.MarshalPKIXPublicKey(certifiedPub)
	if err != nil {
		t.Fatal(err)
	}

	return chainFixture{
		rootPool:   pool,
		chain:      [][]byte{leafDER, rootDER},
		leafKey:    leafKey,
		leafPubKey: pub,
	}
}
