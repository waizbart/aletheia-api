package domain

// AttestationRequest is a platform attestation awaiting verification.
type AttestationRequest struct {
	Platform Platform
	// Challenge is the nonce the server issued; the attestation must prove the
	// key was created for exactly this value.
	Challenge []byte
	// CertChain is the DER-encoded attestation certificate chain, leaf first.
	CertChain [][]byte
}

// AttestationEvidence is what a verified attestation establishes about a
// device. It is stored alongside the device so a later dispute can be answered
// with the exact posture the device had at enrolment.
type AttestationEvidence struct {
	// PublicKeyDER is the attested key in PKIX form. Capture signatures are
	// checked against this key and no other.
	PublicKeyDER []byte
	// Level records where the private key lives.
	Level AttestationLevel
	// PackageName is the application ID that requested the key.
	PackageName string
	// VerifiedBootState is the bootloader posture at attestation time.
	VerifiedBootState string
	// SecurityLevel is the platform's own name for the key's location.
	SecurityLevel string
}
