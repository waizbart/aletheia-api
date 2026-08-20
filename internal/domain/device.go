package domain

import "time"

// Platform identifies the capture platform a device runs.
type Platform string

const (
	PlatformAndroid Platform = "android"
	PlatformIOS     Platform = "ios"
)

// ValidPlatform reports whether p is a platform the registry accepts.
func ValidPlatform(p Platform) bool {
	return p == PlatformAndroid || p == PlatformIOS
}

// AttestationLevel records where the capture key lives. Only hardware-backed
// levels carry provenance weight: a software key can be extracted from a
// compromised device and used to sign anything.
type AttestationLevel string

const (
	AttestationSoftware  AttestationLevel = "software"
	AttestationTEE       AttestationLevel = "tee"
	AttestationStrongBox AttestationLevel = "strongbox"
)

// attestationRank orders levels by the strength of the guarantee they provide,
// so policy can be expressed as a minimum rather than an enumeration.
var attestationRank = map[AttestationLevel]int{
	AttestationSoftware:  0,
	AttestationTEE:       1,
	AttestationStrongBox: 2,
}

// AtLeast reports whether l meets or exceeds min. An unknown level ranks below
// everything, so an unrecognised value can never satisfy a policy — including
// the weakest one, which a plain map index would have let it tie with.
func (l AttestationLevel) AtLeast(min AttestationLevel) bool {
	rank, ok := attestationRank[l]
	if !ok {
		return false
	}
	return rank >= attestationRank[min]
}

// DeviceStatus is a device's standing in the registry.
type DeviceStatus string

const (
	DeviceActive  DeviceStatus = "active"
	DeviceRevoked DeviceStatus = "revoked"
)

// Device is a capture endpoint enrolled by an organisation: a phone running
// the first-party camera app or a partner app embedding the SDK. The public key
// is the hardware-backed attestation key; the private half never leaves the
// device's secure element.
type Device struct {
	ID               string
	OrgID            string
	Platform         Platform
	PublicKey        []byte // DER-encoded PKIX SubjectPublicKeyInfo
	AttestationLevel AttestationLevel
	Model            string
	Status           DeviceStatus
	RevokedAt        *time.Time
	RevocationReason string
	CreatedAt        time.Time
}

// CanCapture reports whether the device may mint new certificates.
//
// Revocation is deliberately forward-only: a revoked device stops producing
// certificates, but the ones it already produced stay in the registry, flagged
// rather than deleted. Erasing history on revocation would destroy exactly the
// evidence an investigation needs — which captures a compromised device made,
// and when.
func (d *Device) CanCapture() bool {
	return d != nil && d.Status == DeviceActive
}

// Revoke marks the device unusable for future captures.
func (d *Device) Revoke(reason string, at time.Time) {
	d.Status = DeviceRevoked
	d.RevocationReason = reason
	t := at.UTC()
	d.RevokedAt = &t
}
