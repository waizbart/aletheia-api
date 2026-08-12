package domain_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
)

func testMetadata() domain.CaptureMetadata {
	return domain.CaptureMetadata{
		CapturedAt: time.Date(2026, 8, 12, 15, 4, 5, 123456789, time.UTC),
		Model:      "Pixel 8",
		OSVersion:  "14",
		AppVersion: "1.0.0",
	}
}

func TestCaptureSigningPayload_IsDeterministic(t *testing.T) {
	md := testMetadata()
	a := domain.CaptureSigningPayload("abc", "nonce", md)
	b := domain.CaptureSigningPayload("abc", "nonce", md)

	if !bytes.Equal(a, b) {
		t.Fatal("payload must be byte-identical for identical inputs")
	}
}

func TestCaptureSigningPayload_NormalisesTimeZone(t *testing.T) {
	utc := testMetadata()

	shifted := utc
	loc := time.FixedZone("BRT", -3*60*60)
	shifted.CapturedAt = utc.CapturedAt.In(loc)

	if !bytes.Equal(
		domain.CaptureSigningPayload("h", "n", utc),
		domain.CaptureSigningPayload("h", "n", shifted),
	) {
		t.Fatal("the same instant in a different zone must produce the same payload")
	}
}

// TestCaptureSigningPayload_IsUnambiguous is the reason fields are
// length-prefixed instead of separator-joined: two different captures must
// never produce the same bytes to sign.
func TestCaptureSigningPayload_IsUnambiguous(t *testing.T) {
	md := testMetadata()

	shifted := md
	shifted.Model = "Pixel 8" + "\n" + "14"
	shifted.OSVersion = ""

	if bytes.Equal(
		domain.CaptureSigningPayload("h", "n", md),
		domain.CaptureSigningPayload("h", "n", shifted),
	) {
		t.Fatal("field contents must not be able to impersonate a field boundary")
	}
}

func TestCaptureSigningPayload_CoversEveryField(t *testing.T) {
	base := domain.CaptureSigningPayload("hash", "nonce", testMetadata())

	mutations := map[string][]byte{
		"content hash": domain.CaptureSigningPayload("other", "nonce", testMetadata()),
		"nonce":        domain.CaptureSigningPayload("hash", "other", testMetadata()),
	}

	md := testMetadata()
	md.CapturedAt = md.CapturedAt.Add(time.Nanosecond)
	mutations["captured at"] = domain.CaptureSigningPayload("hash", "nonce", md)

	md = testMetadata()
	md.Model = "iPhone"
	mutations["model"] = domain.CaptureSigningPayload("hash", "nonce", md)

	md = testMetadata()
	md.OSVersion = "15"
	mutations["os version"] = domain.CaptureSigningPayload("hash", "nonce", md)

	md = testMetadata()
	md.AppVersion = "2.0.0"
	mutations["app version"] = domain.CaptureSigningPayload("hash", "nonce", md)

	for field, mutated := range mutations {
		if bytes.Equal(base, mutated) {
			t.Errorf("%s is not covered by the signed payload", field)
		}
	}
}

func TestCaptureSigningPayload_StartsWithDomainSeparator(t *testing.T) {
	got := domain.CaptureSigningPayload("hash", "nonce", testMetadata())
	if !bytes.HasPrefix(got, []byte("aletheia-capture-v1")) {
		t.Fatal("payload must be domain-separated so a signature cannot be replayed from another protocol")
	}
}

func TestVerifyCaptureSignature(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	payload := domain.CaptureSigningPayload("hash", "nonce", testMetadata())
	digest := sha256.Sum256(payload)
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}

	t.Run("accepts a genuine signature", func(t *testing.T) {
		if err := domain.VerifyCaptureSignature(pubDER, payload, sig); err != nil {
			t.Fatalf("VerifyCaptureSignature: %v", err)
		}
	})

	t.Run("rejects a payload the device did not sign", func(t *testing.T) {
		other := domain.CaptureSigningPayload("tampered", "nonce", testMetadata())
		err := domain.VerifyCaptureSignature(pubDER, other, sig)
		if !errors.Is(err, domain.ErrCaptureSignature) {
			t.Fatalf("error = %v, want ErrCaptureSignature", err)
		}
	})

	t.Run("rejects another device's key", func(t *testing.T) {
		other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		otherDER, _ := x509.MarshalPKIXPublicKey(&other.PublicKey)

		err := domain.VerifyCaptureSignature(otherDER, payload, sig)
		if !errors.Is(err, domain.ErrCaptureSignature) {
			t.Fatalf("error = %v, want ErrCaptureSignature", err)
		}
	})

	t.Run("rejects a corrupted signature", func(t *testing.T) {
		bad := bytes.Clone(sig)
		bad[len(bad)-1] ^= 0xff

		if err := domain.VerifyCaptureSignature(pubDER, payload, bad); err == nil {
			t.Fatal("expected verification to fail")
		}
	})

	t.Run("rejects an unparseable key", func(t *testing.T) {
		if err := domain.VerifyCaptureSignature([]byte("not a key"), payload, sig); err == nil {
			t.Fatal("expected a parse error")
		}
	})

	t.Run("rejects a non-ECDSA key", func(t *testing.T) {
		rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		rsaDER, _ := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)

		err = domain.VerifyCaptureSignature(rsaDER, payload, sig)
		if err == nil {
			t.Fatal("expected an algorithm error")
		}
	})
}
