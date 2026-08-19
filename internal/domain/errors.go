package domain

import "errors"

var (
	ErrAlreadyCertified = errors.New("content already certified")
	ErrNotFound         = errors.New("certificate not found")

	// ErrNonceUnusable covers a challenge that is unknown, already spent or
	// past its expiry. The three are deliberately indistinguishable to the
	// caller: telling an attacker which one applies helps them probe the
	// nonce space.
	ErrNonceUnusable = errors.New("capture challenge is not usable")

	// ErrDeviceNotFound reports a capture referencing an unenrolled device.
	ErrDeviceNotFound = errors.New("device not enrolled")

	// ErrDeviceRevoked reports a capture from a device that has been revoked.
	// Its existing certificates remain valid and queryable; only new captures
	// are refused.
	ErrDeviceRevoked = errors.New("device is revoked")

	// ErrDeviceKeyInUse reports an enrolment whose attested key is already
	// bound to another organisation's device. One hardware key means one
	// device record, or revocation would only ever apply to a row the device
	// could replace.
	ErrDeviceKeyInUse = errors.New("attested key is already enrolled")

	// ErrQuotaExceeded reports an organisation over its plan allowance.
	ErrQuotaExceeded = errors.New("plan quota exceeded")

	// ErrInvalidInput reports a request the caller can fix by sending something
	// different. It exists so an adapter can tell a bad request apart from a
	// failing dependency, instead of reporting a database outage as a 400.
	ErrInvalidInput = errors.New("invalid input")

	// ErrUnauthorized reports a caller whose credentials do not grant access to
	// the requested resource.
	ErrUnauthorized = errors.New("unauthorized")
)
