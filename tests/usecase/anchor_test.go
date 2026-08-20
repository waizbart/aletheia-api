package usecase_test

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

type mockAnchorRepo struct {
	pending   []*domain.Certificate
	pendingFn func(ctx context.Context, limit int) ([]*domain.Certificate, error)
	saveFn    func(ctx context.Context, a *domain.Anchor, leaves []*domain.Certificate) error

	savedAnchor *domain.Anchor
	savedLeaves []*domain.Certificate
	sawLimit    int

	unconfirmedFn func(ctx context.Context, a *domain.Anchor) error
	unconfirmed   []*domain.Anchor
}

func (m *mockAnchorRepo) PendingLeaves(ctx context.Context, limit int) ([]*domain.Certificate, error) {
	m.sawLimit = limit
	if m.pendingFn != nil {
		return m.pendingFn(ctx, limit)
	}
	return m.pending, nil
}

func (m *mockAnchorRepo) SaveAnchor(ctx context.Context, a *domain.Anchor, leaves []*domain.Certificate) error {
	m.savedAnchor, m.savedLeaves = a, leaves
	if m.saveFn != nil {
		return m.saveFn(ctx, a, leaves)
	}
	a.ID = "anchor-1"
	return nil
}

func (m *mockAnchorRepo) SaveUnconfirmedAnchor(ctx context.Context, a *domain.Anchor) error {
	m.unconfirmed = append(m.unconfirmed, a)
	if m.unconfirmedFn != nil {
		return m.unconfirmedFn(ctx, a)
	}
	a.ID = "anchor-unconfirmed"
	return nil
}

type mockAnchorChain struct {
	root      [32]byte
	leafCount uint64
	err       error
	// errTxHash is what a broadcast-but-unconfirmed transaction reports
	// alongside its error.
	errTxHash string
	calls     int
}

func (m *mockAnchorChain) RegisterRoot(_ context.Context, root [32]byte, leafCount uint64) (string, uint64, error) {
	m.calls++
	m.root, m.leafCount = root, leafCount
	if m.err != nil {
		return m.errTxHash, 0, m.err
	}
	return "0xtx", 42, nil
}

func pendingCert(n byte) *domain.Certificate {
	var commitment [32]byte
	commitment[0] = n
	content := make([]byte, 32)
	content[0] = n
	return &domain.Certificate{
		ID:                "cert-" + string(rune('a'+n)),
		ContentHash:       hex.EncodeToString(content),
		FeatureCommitment: &commitment,
	}
}

func pendingCerts(n int) []*domain.Certificate {
	out := make([]*domain.Certificate, n)
	for i := range out {
		out[i] = pendingCert(byte(i + 1))
	}
	return out
}

func TestAnchorUseCase_AnchorsBatchAndAttachesProofs(t *testing.T) {
	repo := &mockAnchorRepo{pending: pendingCerts(5)}
	chain := &mockAnchorChain{}
	uc := usecase.NewAnchorUseCase(repo, chain, 100, fixedClock())

	anchor, err := uc.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// One transaction for the whole batch — this is the entire point.
	if chain.calls != 1 {
		t.Errorf("chain calls = %d, want 1 for the batch", chain.calls)
	}
	if chain.leafCount != 5 {
		t.Errorf("leaf count sent on chain = %d, want 5", chain.leafCount)
	}
	if anchor.TxHash != "0xtx" || anchor.BlockNumber != 42 {
		t.Errorf("anchor = %+v, want the receipt's tx and block", anchor)
	}
	if anchor.Status != domain.AnchorConfirmed || anchor.ConfirmedAt == nil {
		t.Error("an anchor with a receipt is confirmed")
	}

	// Every certificate must carry a proof that verifies against the root that
	// actually went on chain.
	for i, cert := range repo.savedLeaves {
		if cert.LeafIndex != i {
			t.Errorf("cert %d has leaf index %d", i, cert.LeafIndex)
		}
		if cert.TxHash != "0xtx" || cert.BlockNumber != 42 {
			t.Errorf("cert %d did not record the anchor transaction", i)
		}

		var content [32]byte
		raw, _ := hex.DecodeString(cert.ContentHash)
		copy(content[:], raw)
		leaf := domain.MerkleLeaf(content, *cert.FeatureCommitment)

		if !domain.VerifyMerkleProof(leaf, chain.root, domain.ProofFromBytes(cert.MerkleProof)) {
			t.Errorf("cert %d's proof does not verify against the anchored root", i)
		}
	}
}

func TestAnchorUseCase_NothingPending(t *testing.T) {
	repo := &mockAnchorRepo{}
	chain := &mockAnchorChain{}
	uc := usecase.NewAnchorUseCase(repo, chain, 0, fixedClock())

	anchor, err := uc.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if anchor != nil {
		t.Error("an empty backlog produces no anchor")
	}
	if chain.calls != 0 {
		t.Error("an empty backlog must not spend a transaction")
	}
	if repo.sawLimit != 4096 {
		t.Errorf("default batch size = %d, want 4096", repo.sawLimit)
	}
}

// TestAnchorUseCase_LeavesBatchPendingOnChainFailure pins the safe direction to
// fail: re-anchoring costs one extra transaction, whereas marking certificates
// anchored against a root that never landed hands out proofs of nothing.
func TestAnchorUseCase_LeavesBatchPendingOnChainFailure(t *testing.T) {
	repo := &mockAnchorRepo{pending: pendingCerts(3)}
	chain := &mockAnchorChain{err: errors.New("rpc unreachable")}
	uc := usecase.NewAnchorUseCase(repo, chain, 10, fixedClock())

	_, err := uc.RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected the chain failure to surface")
	}
	if repo.savedAnchor != nil {
		t.Fatal("no anchor may be recorded when the transaction did not confirm")
	}
	if len(repo.unconfirmed) != 0 {
		t.Fatal("nothing was broadcast, so there is no transaction to reconcile")
	}
}

// TestAnchorUseCase_RecordsBroadcastButUnconfirmedTransaction: when the
// transaction went out but the receipt never came, the hash is the only handle
// an operator has on a root that may still be mined. The batch still stays
// pending — the record exists for reconciliation, not to claim inclusion.
func TestAnchorUseCase_RecordsBroadcastButUnconfirmedTransaction(t *testing.T) {
	repo := &mockAnchorRepo{pending: pendingCerts(3)}
	chain := &mockAnchorChain{err: errors.New("receipt timeout"), errTxHash: "0xdeadbeef"}
	uc := usecase.NewAnchorUseCase(repo, chain, 10, fixedClock())

	if _, err := uc.RunOnce(context.Background()); err == nil {
		t.Fatal("expected the chain failure to surface")
	}

	if len(repo.unconfirmed) != 1 {
		t.Fatalf("unconfirmed records = %d, want 1", len(repo.unconfirmed))
	}
	rec := repo.unconfirmed[0]
	if rec.TxHash != "0xdeadbeef" {
		t.Errorf("tx hash = %q, want the broadcast hash", rec.TxHash)
	}
	if rec.Status != domain.AnchorPending {
		t.Errorf("status = %q, want %q", rec.Status, domain.AnchorPending)
	}
	if rec.LeafCount != 3 {
		t.Errorf("leaf count = %d, want 3", rec.LeafCount)
	}
	if rec.BlockNumber != 0 {
		t.Errorf("block number = %d, want 0 — no receipt was seen", rec.BlockNumber)
	}
	if repo.savedAnchor != nil {
		t.Fatal("certificates must stay pending: the root may never land")
	}
}

// The bookkeeping is best effort. Failing it must not change the outcome of the
// pass, which was already going to leave the batch pending.
func TestAnchorUseCase_UnconfirmedRecordFailureIsNotFatal(t *testing.T) {
	repo := &mockAnchorRepo{
		pending: pendingCerts(2),
		unconfirmedFn: func(context.Context, *domain.Anchor) error {
			return errors.New("db down")
		},
	}
	chain := &mockAnchorChain{err: errors.New("receipt timeout"), errTxHash: "0xabc"}
	uc := usecase.NewAnchorUseCase(repo, chain, 10, fixedClock())

	_, err := uc.RunOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "registering root") {
		t.Fatalf("error = %v, want the original chain failure to survive", err)
	}
}

// A cancelled context is the usual reason the receipt wait failed, so the
// bookkeeping write must not inherit that cancellation.
func TestAnchorUseCase_RecordsUnconfirmedEvenWhenContextIsCancelled(t *testing.T) {
	repo := &mockAnchorRepo{pending: pendingCerts(1)}
	chain := &mockAnchorChain{err: context.Canceled, errTxHash: "0xfeed"}
	uc := usecase.NewAnchorUseCase(repo, chain, 10, fixedClock())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := uc.RunOnce(ctx); err == nil {
		t.Fatal("expected the chain failure to surface")
	}
	if len(repo.unconfirmed) != 1 {
		t.Fatalf("unconfirmed records = %d, want 1 despite the cancelled context", len(repo.unconfirmed))
	}
}

func TestAnchorUseCase_PropagatesRepositoryFailures(t *testing.T) {
	t.Run("reading the backlog", func(t *testing.T) {
		repo := &mockAnchorRepo{pendingFn: func(context.Context, int) ([]*domain.Certificate, error) {
			return nil, errors.New("db down")
		}}
		uc := usecase.NewAnchorUseCase(repo, &mockAnchorChain{}, 10, fixedClock())

		if _, err := uc.RunOnce(context.Background()); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("saving the batch", func(t *testing.T) {
		repo := &mockAnchorRepo{
			pending: pendingCerts(2),
			saveFn: func(context.Context, *domain.Anchor, []*domain.Certificate) error {
				return errors.New("db down")
			},
		}
		uc := usecase.NewAnchorUseCase(repo, &mockAnchorChain{}, 10, fixedClock())

		_, err := uc.RunOnce(context.Background())
		if err == nil || !strings.Contains(err.Error(), "saving batch") {
			t.Fatalf("error = %v, want a save failure", err)
		}
	})
}

// TestAnchorUseCase_ToleratesIncompleteCertificates: a legacy row with a
// malformed hash or no commitment still has to land in a batch rather than
// stall the worker forever.
func TestAnchorUseCase_ToleratesIncompleteCertificates(t *testing.T) {
	repo := &mockAnchorRepo{pending: []*domain.Certificate{
		{ID: "cert-legacy", ContentHash: "not-hex"},
		pendingCert(1),
	}}
	chain := &mockAnchorChain{}
	uc := usecase.NewAnchorUseCase(repo, chain, 10, fixedClock())

	if _, err := uc.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if chain.leafCount != 2 {
		t.Errorf("leaf count = %d, want both certificates anchored", chain.leafCount)
	}
}

func TestAnchorUseCase_RunStopsOnContextCancel(t *testing.T) {
	repo := &mockAnchorRepo{}
	uc := usecase.NewAnchorUseCase(repo, &mockAnchorChain{}, 10, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		uc.Run(ctx, time.Millisecond)
		close(done)
	}()

	cancel()
	<-done
}

// TestAnchorUseCase_RunSurvivesAFailedPass: a failing pass must not kill the
// worker. The batch stays pending and the next tick retries it.
func TestAnchorUseCase_RunSurvivesAFailedPass(t *testing.T) {
	attempts := make(chan struct{}, 4)
	repo := &mockAnchorRepo{pendingFn: func(context.Context, int) ([]*domain.Certificate, error) {
		select {
		case attempts <- struct{}{}:
		default:
		}
		return nil, errors.New("db down")
	}}
	uc := usecase.NewAnchorUseCase(repo, &mockAnchorChain{}, 10, fixedClock())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		uc.Run(ctx, time.Millisecond)
		close(done)
	}()

	// Two ticks proves the loop kept running after the first failure.
	<-attempts
	<-attempts
	cancel()
	<-done
}
