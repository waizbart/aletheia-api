// Package anchor contains the background worker that anchors certificates on
// chain asynchronously. Certify persists a row with AnchorStatus "pending"; this
// worker polls the outbox, calls the blockchain service, and records the result.
package anchor

import (
	"context"
	"encoding/hex"
	"log"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

// Worker drains the pending-anchor outbox on an interval.
type Worker struct {
	repo        usecase.CertificateRepository
	chain       usecase.BlockchainService
	interval    time.Duration
	batch       int
	maxAttempts int
}

func NewWorker(repo usecase.CertificateRepository, chain usecase.BlockchainService, interval time.Duration, batch, maxAttempts int) *Worker {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if batch <= 0 {
		batch = 16
	}
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	return &Worker{repo: repo, chain: chain, interval: interval, batch: batch, maxAttempts: maxAttempts}
}

// Run polls until ctx is cancelled, processing one batch per tick. It returns
// ctx.Err() on shutdown so the caller can wait for a clean exit.
func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.ProcessBatch(ctx); err != nil {
				log.Printf("anchor worker: %v", err)
			}
		}
	}
}

// ProcessBatch claims a batch of pending certificates and anchors each. Exposed
// so a single batch can be driven deterministically in tests.
func (w *Worker) ProcessBatch(ctx context.Context) error {
	pending, err := w.repo.FindPendingAnchors(ctx, w.batch, w.maxAttempts)
	if err != nil {
		return err
	}
	for _, cert := range pending {
		w.anchorOne(ctx, cert)
	}
	return nil
}

func (w *Worker) anchorOne(ctx context.Context, cert *domain.Certificate) {
	commitmentHex := ""
	if cert.FeatureCommitment != nil {
		commitmentHex = hex.EncodeToString(cert.FeatureCommitment[:])
	}

	// Guard against re-spending gas after a crash mid-send: if the hash is
	// already registered on chain we still need the tx hash, so we only skip the
	// send when the chain reports it registered AND we already have a tx hash.
	if cert.TxHash != "" {
		if registered, qerr := w.chain.IsHashRegistered(ctx, cert.ContentHash); qerr == nil && registered {
			if err := w.repo.MarkAnchored(ctx, cert.ID, cert.TxHash, cert.BlockNumber); err != nil {
				log.Printf("anchor worker: mark anchored (already registered) %s: %v", cert.ID, err)
			}
			return
		}
	}

	txHash, blockNum, err := w.chain.RegisterHash(ctx, cert.ContentHash, commitmentHex)
	if err != nil {
		if merr := w.repo.MarkAnchorFailed(ctx, cert.ID, err.Error(), w.maxAttempts); merr != nil {
			log.Printf("anchor worker: mark failed %s: %v", cert.ID, merr)
		}
		return
	}
	if err := w.repo.MarkAnchored(ctx, cert.ID, txHash, blockNum); err != nil {
		log.Printf("anchor worker: mark anchored %s: %v", cert.ID, err)
	}
}
