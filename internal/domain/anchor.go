package domain

import "time"

// AnchorStatus tracks a batch through its lifecycle.
type AnchorStatus string

const (
	// AnchorPending means the transaction was broadcast but has no receipt yet.
	AnchorPending AnchorStatus = "pending"
	// AnchorConfirmed means a receipt was retrieved and the block number is
	// real rather than assumed.
	AnchorConfirmed AnchorStatus = "confirmed"
	// AnchorFailed means the transaction was mined but reverted.
	AnchorFailed AnchorStatus = "failed"
)

// Anchor is one on-chain commitment covering a batch of certificates.
//
// Batching is why the chain layer scales: the cost of an anchor does not change
// with the number of certificates under it, and each certificate keeps an
// inclusion proof that anyone can check against the root without trusting this
// API.
type Anchor struct {
	ID          string
	Root        [32]byte
	LeafCount   int
	TxHash      string
	BlockNumber uint64
	Status      AnchorStatus
	CreatedAt   time.Time
	ConfirmedAt *time.Time
}

// Confirmed reports whether the batch is committed and its block number real.
func (a *Anchor) Confirmed() bool { return a != nil && a.Status == AnchorConfirmed }
