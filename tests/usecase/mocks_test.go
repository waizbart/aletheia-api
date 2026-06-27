package usecase_test

import (
	"context"
	"errors"

	"github.com/waizbart/aletheia-api/internal/domain"
)

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read error") }

type mockRepo struct {
	saveFn                    func(ctx context.Context, cert *domain.Certificate) error
	findByHashFn              func(ctx context.Context, hash string) (*domain.Certificate, error)
	findCandidatesByPHashesFn func(ctx context.Context, phashes [][32]byte, maxDistance, topK int) ([]*domain.Certificate, error)
	deleteFn                  func(ctx context.Context, hash string) error
	findPendingAnchorsFn      func(ctx context.Context, limit, maxAttempts int) ([]*domain.Certificate, error)
	markAnchoredFn            func(ctx context.Context, id, txHash string, blockNum uint64) error
	markAnchorFailedFn        func(ctx context.Context, id, errMsg string, maxAttempts int) error
}

func (m *mockRepo) Save(ctx context.Context, cert *domain.Certificate) error {
	return m.saveFn(ctx, cert)
}

func (m *mockRepo) FindByHash(ctx context.Context, hash string) (*domain.Certificate, error) {
	return m.findByHashFn(ctx, hash)
}

func (m *mockRepo) FindCandidatesByPHashes(ctx context.Context, phashes [][32]byte, maxDistance, topK int) ([]*domain.Certificate, error) {
	if m.findCandidatesByPHashesFn == nil {
		return nil, nil
	}
	return m.findCandidatesByPHashesFn(ctx, phashes, maxDistance, topK)
}

func (m *mockRepo) Delete(ctx context.Context, hash string) error {
	if m.deleteFn == nil {
		return nil
	}
	return m.deleteFn(ctx, hash)
}

func (m *mockRepo) FindPendingAnchors(ctx context.Context, limit, maxAttempts int) ([]*domain.Certificate, error) {
	if m.findPendingAnchorsFn == nil {
		return nil, nil
	}
	return m.findPendingAnchorsFn(ctx, limit, maxAttempts)
}

func (m *mockRepo) MarkAnchored(ctx context.Context, id, txHash string, blockNum uint64) error {
	if m.markAnchoredFn == nil {
		return nil
	}
	return m.markAnchoredFn(ctx, id, txHash, blockNum)
}

func (m *mockRepo) MarkAnchorFailed(ctx context.Context, id, errMsg string, maxAttempts int) error {
	if m.markAnchorFailedFn == nil {
		return nil
	}
	return m.markAnchorFailedFn(ctx, id, errMsg, maxAttempts)
}

type mockExtractor struct {
	computeFn func(ctx context.Context, content []byte) (*domain.FeatureSignature, []byte, error)
	matchFn   func(ctx context.Context, refSig, candSig *domain.FeatureSignature, refImage, candImage []byte) (domain.MatchDecision, error)
}

func (m *mockExtractor) Compute(ctx context.Context, content []byte) (*domain.FeatureSignature, []byte, error) {
	if m.computeFn == nil {
		return &domain.FeatureSignature{Descriptors: []byte{0x01}, Keypoints: []byte{0x02}}, []byte("jpeg"), nil
	}
	return m.computeFn(ctx, content)
}

func (m *mockExtractor) Match(ctx context.Context, refSig, candSig *domain.FeatureSignature, refImage, candImage []byte) (domain.MatchDecision, error) {
	if m.matchFn == nil {
		return domain.MatchDecision{}, nil
	}
	return m.matchFn(ctx, refSig, candSig, refImage, candImage)
}

type mockBlobStore struct {
	putFn    func(ctx context.Context, key string, data []byte) error
	getFn    func(ctx context.Context, key string) ([]byte, error)
	deleteFn func(ctx context.Context, key string) error
}

func (m *mockBlobStore) Put(ctx context.Context, key string, data []byte) error {
	if m.putFn == nil {
		return nil
	}
	return m.putFn(ctx, key, data)
}

func (m *mockBlobStore) Get(ctx context.Context, key string) ([]byte, error) {
	if m.getFn == nil {
		return []byte("ref-jpeg"), nil
	}
	return m.getFn(ctx, key)
}

func (m *mockBlobStore) Delete(ctx context.Context, key string) error {
	if m.deleteFn == nil {
		return nil
	}
	return m.deleteFn(ctx, key)
}
