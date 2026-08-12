package usecase

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
)

// defaultBatchSize bounds how many certificates one anchor commits. The cost of
// a transaction does not grow with the batch, but the proof each certificate
// carries grows with log2(size), and a very large batch delays the certificates
// at the front of it.
const defaultBatchSize = 4096

// AnchorUseCase commits pending certificates to the chain in batches.
//
// Certification no longer waits on the chain. A capture is certified as soon as
// it is persisted, and this worker anchors it shortly after — which removes a
// network round trip from the request path and turns one transaction per
// certificate into one per batch.
type AnchorUseCase struct {
	repo      AnchorRepository
	chain     BlockchainService
	batchSize int
	now       func() time.Time
}

func NewAnchorUseCase(repo AnchorRepository, chain BlockchainService, batchSize int, now func() time.Time) *AnchorUseCase {
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	if now == nil {
		now = time.Now
	}
	return &AnchorUseCase{repo: repo, chain: chain, batchSize: batchSize, now: now}
}

// RunOnce anchors one batch. It returns a nil anchor when there is nothing
// pending, which is the common case between captures.
func (uc *AnchorUseCase) RunOnce(ctx context.Context) (*domain.Anchor, error) {
	pending, err := uc.repo.PendingLeaves(ctx, uc.batchSize)
	if err != nil {
		return nil, fmt.Errorf("anchor: reading pending certificates: %w", err)
	}
	if len(pending) == 0 {
		return nil, nil
	}

	leaves := make([][32]byte, len(pending))
	for i, cert := range pending {
		leaves[i] = certificateLeaf(cert)
	}
	tree := domain.BuildMerkleTree(leaves)

	txHash, blockNumber, err := uc.chain.RegisterRoot(ctx, tree.Root, uint64(len(leaves)))
	if err != nil {
		// The transaction may still be in flight. Leaving the batch pending is
		// safe: re-anchoring the same certificates costs one extra transaction,
		// whereas marking them anchored on an unconfirmed root would hand out
		// proofs against a root that may never land.
		return nil, fmt.Errorf("anchor: registering root: %w", err)
	}

	anchor := &domain.Anchor{
		Root:        tree.Root,
		LeafCount:   len(leaves),
		TxHash:      txHash,
		BlockNumber: blockNumber,
		Status:      domain.AnchorConfirmed,
		CreatedAt:   uc.now().UTC(),
	}
	confirmedAt := uc.now().UTC()
	anchor.ConfirmedAt = &confirmedAt

	for i, cert := range pending {
		cert.TxHash = txHash
		cert.BlockNumber = blockNumber
		cert.LeafIndex = i
		cert.MerkleProof = domain.FlattenProof(tree.Proofs[i])
	}

	if err := uc.repo.SaveAnchor(ctx, anchor, pending); err != nil {
		return nil, fmt.Errorf("anchor: saving batch: %w", err)
	}

	log.Printf("anchored %d certificates as %s in block %d",
		len(pending), hex.EncodeToString(tree.Root[:]), blockNumber)

	return anchor, nil
}

// Run anchors on an interval until the context is cancelled.
func (uc *AnchorUseCase) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := uc.RunOnce(ctx); err != nil && ctx.Err() == nil {
				// A failed pass is not fatal: the batch stays pending and the
				// next tick retries it.
				log.Printf("anchor worker: %v", err)
			}
		}
	}
}

// certificateLeaf derives a certificate's Merkle leaf from the two values that
// identify it: what was certified, and the commitment to its feature bundle.
func certificateLeaf(cert *domain.Certificate) [32]byte {
	var contentHash, commitment [32]byte

	if raw, err := hex.DecodeString(cert.ContentHash); err == nil && len(raw) == 32 {
		copy(contentHash[:], raw)
	}
	if cert.FeatureCommitment != nil {
		commitment = *cert.FeatureCommitment
	}
	return domain.MerkleLeaf(contentHash, commitment)
}
