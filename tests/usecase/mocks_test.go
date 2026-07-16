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

type mockBlockchain struct {
	registerHashFn     func(ctx context.Context, contentHash, featureCommitment string) (string, uint64, error)
	isHashRegisteredFn func(ctx context.Context, hash string) (bool, error)
}

func (m *mockBlockchain) RegisterHash(ctx context.Context, contentHash, featureCommitment string) (string, uint64, error) {
	return m.registerHashFn(ctx, contentHash, featureCommitment)
}

func (m *mockBlockchain) IsHashRegistered(ctx context.Context, hash string) (bool, error) {
	return m.isHashRegisteredFn(ctx, hash)
}

// validColorGrid returns a well-formed color grid for signatures used in
// tests: correct byte length with an arbitrary fill.
func validColorGrid() []byte {
	grid := make([]byte, domain.ColorGridBytes)
	for i := range grid {
		grid[i] = byte(i % 251)
	}
	return grid
}

// signatureWithGrid builds a complete stored signature (descriptors,
// keypoints, color grid, reference dims) as certify would persist it.
func signatureWithGrid() *domain.FeatureSignature {
	return &domain.FeatureSignature{
		Descriptors: []byte{0x01},
		Keypoints:   []byte{0x02},
		ColorGrid:   validColorGrid(),
		RefWidth:    1024,
		RefHeight:   768,
	}
}

type mockExtractor struct {
	computeFn func(ctx context.Context, content []byte) (*domain.FeatureSignature, error)
	matchFn   func(ctx context.Context, refSig, candSig *domain.FeatureSignature, candImage []byte) (domain.MatchDecision, error)
}

func (m *mockExtractor) Compute(ctx context.Context, content []byte) (*domain.FeatureSignature, error) {
	if m.computeFn == nil {
		return signatureWithGrid(), nil
	}
	return m.computeFn(ctx, content)
}

func (m *mockExtractor) Match(ctx context.Context, refSig, candSig *domain.FeatureSignature, candImage []byte) (domain.MatchDecision, error) {
	if m.matchFn == nil {
		return domain.MatchDecision{}, nil
	}
	return m.matchFn(ctx, refSig, candSig, candImage)
}

type mockRenderer struct {
	renderFn func(grid []byte, refWidth, refHeight int) ([]byte, error)
}

func (m *mockRenderer) RenderColorGridPNG(grid []byte, refWidth, refHeight int) ([]byte, error) {
	if m.renderFn == nil {
		return []byte("png"), nil
	}
	return m.renderFn(grid, refWidth, refHeight)
}
