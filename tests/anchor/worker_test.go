package anchor_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waizbart/aletheia-api/internal/anchor"
	"github.com/waizbart/aletheia-api/internal/domain"
)

// anchorRepo is a minimal CertificateRepository implementation exercising only
// the methods the worker calls; the rest satisfy the interface.
type anchorRepo struct {
	pending         []*domain.Certificate
	findErr         error
	anchored        map[string]anchoredRec
	failed          map[string]string
	markAnchoredErr error
	markFailedErr   error
}

type anchoredRec struct {
	txHash   string
	blockNum uint64
}

func newAnchorRepo(pending ...*domain.Certificate) *anchorRepo {
	return &anchorRepo{
		pending:  pending,
		anchored: map[string]anchoredRec{},
		failed:   map[string]string{},
	}
}

func (r *anchorRepo) FindPendingAnchors(_ context.Context, limit, _ int) ([]*domain.Certificate, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	out := r.pending
	r.pending = nil // claimed once, like FOR UPDATE SKIP LOCKED + status flip
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *anchorRepo) MarkAnchored(_ context.Context, id, txHash string, blockNum uint64) error {
	if r.markAnchoredErr != nil {
		return r.markAnchoredErr
	}
	r.anchored[id] = anchoredRec{txHash: txHash, blockNum: blockNum}
	return nil
}

func (r *anchorRepo) MarkAnchorFailed(_ context.Context, id, errMsg string, _ int) error {
	r.failed[id] = errMsg
	return r.markFailedErr
}

// Unused-by-worker methods to satisfy usecase.CertificateRepository.
func (r *anchorRepo) Save(context.Context, *domain.Certificate) error { return nil }
func (r *anchorRepo) FindByHash(context.Context, string) (*domain.Certificate, error) {
	return nil, nil
}
func (r *anchorRepo) FindCandidatesByPHashes(context.Context, [][32]byte, int, int) ([]*domain.Certificate, error) {
	return nil, nil
}
func (r *anchorRepo) Delete(context.Context, string) error { return nil }

type anchorChain struct {
	registerFn   func(ctx context.Context, contentHash, commitment string) (string, uint64, error)
	registeredFn func(ctx context.Context, hash string) (bool, error)
	calls        int
}

func (c *anchorChain) RegisterHash(ctx context.Context, contentHash, commitment string) (string, uint64, error) {
	c.calls++
	if c.registerFn == nil {
		return "0xtx-" + contentHash, 7, nil
	}
	return c.registerFn(ctx, contentHash, commitment)
}

func (c *anchorChain) IsHashRegistered(ctx context.Context, hash string) (bool, error) {
	if c.registeredFn == nil {
		return false, nil
	}
	return c.registeredFn(ctx, hash)
}

func pendingCert(id, hash string) *domain.Certificate {
	commit := [32]byte{0x01, 0x02}
	return &domain.Certificate{
		ID:                id,
		ContentHash:       hash,
		FeatureCommitment: &commit,
		AnchorStatus:      domain.AnchorPending,
	}
}

func TestWorker_ProcessBatch_AnchorsPending(t *testing.T) {
	repo := newAnchorRepo(pendingCert("id-1", "hashaaaa"), pendingCert("id-2", "hashbbbb"))
	chain := &anchorChain{
		registerFn: func(_ context.Context, contentHash, _ string) (string, uint64, error) {
			return "0xtx-" + contentHash, 42, nil
		},
	}
	w := anchor.NewWorker(repo, chain, time.Second, 16, 5)

	if err := w.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("processBatch: %v", err)
	}

	if chain.calls != 2 {
		t.Fatalf("RegisterHash calls = %d, want 2", chain.calls)
	}
	if rec, ok := repo.anchored["id-1"]; !ok || rec.txHash != "0xtx-hashaaaa" || rec.blockNum != 42 {
		t.Fatalf("id-1 not anchored correctly: %+v ok=%v", rec, ok)
	}
	if _, ok := repo.anchored["id-2"]; !ok {
		t.Fatal("id-2 not anchored")
	}
	if len(repo.failed) != 0 {
		t.Fatalf("expected no failures, got %v", repo.failed)
	}
}

func TestWorker_ProcessBatch_MarksFailedOnChainError(t *testing.T) {
	repo := newAnchorRepo(pendingCert("id-1", "hashaaaa"))
	chain := &anchorChain{
		registerFn: func(_ context.Context, _, _ string) (string, uint64, error) {
			return "", 0, errors.New("rpc down")
		},
	}
	w := anchor.NewWorker(repo, chain, time.Second, 16, 5)

	if err := w.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("processBatch: %v", err)
	}

	if _, ok := repo.anchored["id-1"]; ok {
		t.Fatal("id-1 should not be anchored on chain error")
	}
	if msg, ok := repo.failed["id-1"]; !ok || msg != "rpc down" {
		t.Fatalf("id-1 failure not recorded: msg=%q ok=%v", msg, ok)
	}
}

func TestWorker_ProcessBatch_SkipsRegisterWhenAlreadyRegistered(t *testing.T) {
	cert := pendingCert("id-1", "hashaaaa")
	cert.TxHash = "0xprior"
	cert.BlockNumber = 9
	repo := newAnchorRepo(cert)
	chain := &anchorChain{
		registeredFn: func(_ context.Context, _ string) (bool, error) { return true, nil },
	}
	w := anchor.NewWorker(repo, chain, time.Second, 16, 5)

	if err := w.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("processBatch: %v", err)
	}

	if chain.calls != 0 {
		t.Fatalf("RegisterHash should not be called, got %d calls", chain.calls)
	}
	if rec, ok := repo.anchored["id-1"]; !ok || rec.txHash != "0xprior" || rec.blockNum != 9 {
		t.Fatalf("id-1 should be marked anchored from prior tx: %+v ok=%v", rec, ok)
	}
}

func TestWorker_ProcessBatch_AlreadyRegisteredMarkAnchoredErrorTolerated(t *testing.T) {
	cert := pendingCert("id-1", "hashaaaa")
	cert.TxHash = "0xprior"
	repo := newAnchorRepo(cert)
	repo.markAnchoredErr = errors.New("db write failed")
	chain := &anchorChain{
		registeredFn: func(_ context.Context, _ string) (bool, error) { return true, nil },
	}
	w := anchor.NewWorker(repo, chain, time.Second, 16, 5)

	if err := w.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("processBatch: %v", err)
	}
	// Skipped the chain send and tolerated the mark-anchored write error.
	if chain.calls != 0 {
		t.Fatalf("RegisterHash calls = %d, want 0", chain.calls)
	}
}

func TestWorker_ProcessBatch_FindError(t *testing.T) {
	repo := newAnchorRepo()
	repo.findErr = errors.New("db down")
	w := anchor.NewWorker(repo, &anchorChain{}, time.Second, 16, 5)

	if err := w.ProcessBatch(context.Background()); err == nil {
		t.Fatal("expected error from FindPendingAnchors to propagate")
	}
}

func TestWorker_NewWorker_ClampsNonPositiveArgs(t *testing.T) {
	// interval/batch/maxAttempts <= 0 must be clamped to working defaults; a
	// ProcessBatch over an empty repo should simply succeed.
	repo := newAnchorRepo()
	w := anchor.NewWorker(repo, &anchorChain{}, 0, 0, 0)
	if err := w.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("processBatch with clamped args: %v", err)
	}
}

func TestWorker_ProcessBatch_MarkAnchoredErrorIsTolerated(t *testing.T) {
	repo := newAnchorRepo(pendingCert("id-1", "hashaaaa"))
	repo.markAnchoredErr = errors.New("db write failed")
	chain := &anchorChain{}
	w := anchor.NewWorker(repo, chain, time.Second, 16, 5)

	// The mark-anchored error is logged, not returned: the batch completes.
	if err := w.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("processBatch: %v", err)
	}
	if chain.calls != 1 {
		t.Fatalf("RegisterHash calls = %d, want 1", chain.calls)
	}
}

func TestWorker_ProcessBatch_IsHashRegisteredErrorFallsThroughToRegister(t *testing.T) {
	cert := pendingCert("id-1", "hashaaaa")
	cert.TxHash = "0xprior"
	repo := newAnchorRepo(cert)
	chain := &anchorChain{
		registeredFn: func(_ context.Context, _ string) (bool, error) {
			return false, errors.New("rpc query failed")
		},
	}
	w := anchor.NewWorker(repo, chain, time.Second, 16, 5)

	if err := w.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("processBatch: %v", err)
	}
	// IsHashRegistered errored, so the worker proceeds to RegisterHash anyway.
	if chain.calls != 1 {
		t.Fatalf("RegisterHash calls = %d, want 1 (fall through on query error)", chain.calls)
	}
	if _, ok := repo.anchored["id-1"]; !ok {
		t.Fatal("id-1 should be anchored after fall-through register")
	}
}

func TestWorker_ProcessBatch_MarkFailedErrorIsTolerated(t *testing.T) {
	repo := newAnchorRepo(pendingCert("id-1", "hashaaaa"))
	repo.markFailedErr = errors.New("db write failed")
	chain := &anchorChain{
		registerFn: func(_ context.Context, _, _ string) (string, uint64, error) {
			return "", 0, errors.New("rpc down")
		},
	}
	w := anchor.NewWorker(repo, chain, time.Second, 16, 5)

	// Both the chain call and the mark-failed write error; the batch still
	// completes (errors are logged, not returned).
	if err := w.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("processBatch: %v", err)
	}
	if _, ok := repo.failed["id-1"]; !ok {
		t.Fatal("expected mark-failed to have been attempted")
	}
}

func TestWorker_Run_TicksAndLogsBatchError(t *testing.T) {
	repo := newAnchorRepo()
	repo.findErr = errors.New("transient db error")
	w := anchor.NewWorker(repo, &anchorChain{}, 5*time.Millisecond, 16, 5)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	// Run should keep ticking (logging the batch error) until the context expires.
	if err := w.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run returned %v, want context.DeadlineExceeded", err)
	}
}

func TestWorker_Run_StopsOnContextCancel(t *testing.T) {
	repo := newAnchorRepo()
	w := anchor.NewWorker(repo, &anchorChain{}, 10*time.Millisecond, 16, 5)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}
