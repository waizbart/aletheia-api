package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

// Anchor status values for the asynchronous on-chain anchoring outbox.
const (
	AnchorPending   = "pending"
	AnchorAnchoring = "anchoring"
	AnchorAnchored  = "anchored"
	AnchorFailed    = "failed"
)

type Certificate struct {
	ID                string
	ContentHash       string
	PHash             *[32]byte
	Signature         *FeatureSignature
	FeatureCommitment *[32]byte
	ImageBlobKey      string
	Registrant        string
	TxHash            string
	BlockNumber       uint64
	CreatedAt         time.Time

	// AnchorStatus tracks the on-chain anchoring lifecycle: pending -> anchoring
	// -> anchored (or failed). Set to AnchorPending at certify time; the anchor
	// worker advances it.
	AnchorStatus   string
	AnchorAttempts int
	AnchoredAt     *time.Time
}

func HashContent(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", fmt.Errorf("hashing content: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
