package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

type Certificate struct {
	ID                string
	ContentHash       string
	PHash             *[32]byte
	Signature         *FeatureSignature
	FeatureCommitment *[32]byte
	Registrant        string
	TxHash            string
	BlockNumber       uint64
	CreatedAt         time.Time

	// OrgID owns the certificate. Empty on rows predating multi-tenancy.
	OrgID string
	// DeviceID identifies the attested device that captured the content.
	// Empty means the certificate came through the legacy unattested upload
	// path and carries no capture-time provenance.
	DeviceID string
	// CapturedAt is the device-reported capture time, covered by the device
	// signature. Nil for unattested certificates.
	CapturedAt *time.Time

	// AnchorID links the certificate to the batch whose Merkle root was
	// anchored on chain. Empty until the anchor worker picks it up.
	AnchorID string
	// MerkleProof is the inclusion proof of this certificate's leaf in the
	// anchored root, ordered from the leaf's sibling upward.
	MerkleProof [][]byte
	// LeafIndex positions the certificate in the anchored batch, which the
	// proof needs in order to know which side each sibling sits on.
	LeafIndex int
}

// Attested reports whether the certificate came from a hardware-attested
// capture rather than a plain upload. This is the distinction that decides how
// much a verifier should trust it.
func (c *Certificate) Attested() bool { return c != nil && c.DeviceID != "" }

// Anchored reports whether the certificate's batch has been committed on chain.
func (c *Certificate) Anchored() bool { return c != nil && c.TxHash != "" }

func HashContent(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", fmt.Errorf("hashing content: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
