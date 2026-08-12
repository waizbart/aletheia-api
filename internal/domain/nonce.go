package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// CaptureNonceBytes is the challenge length. 32 bytes matches the SHA-256
// output the attestation extension carries and leaves no useful collision
// surface.
const CaptureNonceBytes = 32

// CaptureNonce is a single-use, short-lived challenge that binds a capture to a
// specific moment and organisation.
//
// Without a server-issued challenge, an attacker who once obtained a genuine
// signed capture could replay it forever, and a device could pre-sign captures
// in bulk. The nonce is what makes "signed by this device" mean "signed by this
// device, just now, for us".
type CaptureNonce struct {
	Value      string // hex-encoded, CaptureNonceBytes long
	OrgID      string
	IssuedAt   time.Time
	ExpiresAt  time.Time
	ConsumedAt *time.Time
}

// NewCaptureNonce mints a challenge valid for ttl from now.
func NewCaptureNonce(orgID string, ttl time.Duration, now time.Time) (CaptureNonce, error) {
	if orgID == "" {
		return CaptureNonce{}, fmt.Errorf("nonce: org id is required")
	}
	if ttl <= 0 {
		return CaptureNonce{}, fmt.Errorf("nonce: ttl must be positive")
	}

	raw := make([]byte, CaptureNonceBytes)
	if _, err := rand.Read(raw); err != nil {
		return CaptureNonce{}, fmt.Errorf("nonce: generating challenge: %w", err)
	}

	issued := now.UTC()
	return CaptureNonce{
		Value:     hex.EncodeToString(raw),
		OrgID:     orgID,
		IssuedAt:  issued,
		ExpiresAt: issued.Add(ttl),
	}, nil
}

// Consumed reports whether the nonce has already been spent.
func (n CaptureNonce) Consumed() bool { return n.ConsumedAt != nil }

// Expired reports whether the nonce's validity window has closed.
func (n CaptureNonce) Expired(now time.Time) bool { return !now.UTC().Before(n.ExpiresAt) }

// Usable reports whether the nonce may still back a capture.
func (n CaptureNonce) Usable(now time.Time) bool { return !n.Consumed() && !n.Expired(now) }

// ValidNonceFormat reports whether v has the shape of an issued challenge.
// Checked before any database lookup so malformed input never reaches a query.
func ValidNonceFormat(v string) bool {
	if len(v) != CaptureNonceBytes*2 {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil
}
