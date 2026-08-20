package domain

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// captureSigningDomain separates capture signatures from every other signature
// a device key might ever produce. Without a domain tag, a signature obtained
// in one protocol could be replayed as a valid capture in another.
const captureSigningDomain = "aletheia-capture-v1"

// ErrCaptureSignature reports a capture whose signature does not verify against
// the enrolled device key.
var ErrCaptureSignature = errors.New("capture signature does not verify")

// CaptureMetadata is the contextual data the device signs alongside the image.
// It is covered by the signature, so none of it can be altered in transit or
// rewritten by the server without invalidating the capture.
type CaptureMetadata struct {
	CapturedAt time.Time
	Model      string
	OSVersion  string
	AppVersion string
}

// CaptureSigningPayload builds the exact byte string a device signs.
//
// Every field is length-prefixed with a big-endian uint32 rather than joined by
// a separator: a model name containing the separator would otherwise let two
// different captures produce the same payload. The format is deliberately
// trivial to reimplement in Kotlin and Swift — the SDKs must produce this byte
// string exactly, or nothing verifies.
//
//	"aletheia-capture-v1"
//	uint32(len) || contentHash   (lowercase hex sha256 of the image bytes)
//	uint32(len) || nonce         (hex challenge issued by the server)
//	uint32(len) || capturedAt    (RFC 3339 nanoseconds, UTC)
//	uint32(len) || model
//	uint32(len) || osVersion
//	uint32(len) || appVersion
func CaptureSigningPayload(contentHash, nonce string, md CaptureMetadata) []byte {
	var buf bytes.Buffer
	buf.WriteString(captureSigningDomain)

	for _, field := range []string{
		contentHash,
		nonce,
		md.CapturedAt.UTC().Format(time.RFC3339Nano),
		md.Model,
		md.OSVersion,
		md.AppVersion,
	} {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(field)))
		buf.Write(length[:])
		buf.WriteString(field)
	}

	return buf.Bytes()
}

// VerifyCaptureSignature checks sig over payload using the device's enrolled
// public key.
//
// The key is an ECDSA P-256 key held in the device's secure element (Android
// Keystore / Apple Secure Enclave) and the signature is ASN.1 DER, which is
// what both platforms' SHA256withECDSA implementations emit.
func VerifyCaptureSignature(publicKeyDER, payload, sig []byte) error {
	pub, err := x509.ParsePKIXPublicKey(publicKeyDER)
	if err != nil {
		return fmt.Errorf("capture: parsing device public key: %w", err)
	}

	ecPub, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("capture: device key must be ECDSA, got %T", pub)
	}

	digest := sha256.Sum256(payload)
	if !ecdsa.VerifyASN1(ecPub, digest[:], sig) {
		return ErrCaptureSignature
	}
	return nil
}
