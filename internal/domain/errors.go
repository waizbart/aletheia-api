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

	// ErrVideoTooLarge reports an upload past the byte ceiling. It is raised
	// while the body is still streaming, because a limit that only applies
	// after the bytes have landed is not a limit.
	ErrVideoTooLarge = errors.New("video exceeds the upload size limit")

	// ErrVideoTooLong reports a video past the duration ceiling. Decode cost is
	// linear in duration and certification is synchronous, so the ceiling is
	// what keeps a request inside its deadline.
	ErrVideoTooLong = errors.New("video exceeds the duration limit")

	// ErrVideoResolution reports a frame size past the pixel ceiling, which
	// exists to blunt decoder bombs.
	ErrVideoResolution = errors.New("video exceeds the resolution limit")

	// ErrVideoUndecodable reports a container the decoder could not open or one
	// that yielded no frames. It deliberately does not distinguish the two:
	// both mean there is nothing to certify.
	ErrVideoUndecodable = errors.New("video could not be decoded")
)
