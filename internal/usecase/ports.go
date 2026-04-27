package usecase

import (
	"context"

	"github.com/waizbart/aletheia-api/internal/domain"
)

type CertificateRepository interface {
	Save(ctx context.Context, cert *domain.Certificate) error
	FindByHash(ctx context.Context, contentHash string) (*domain.Certificate, error)
	FindCandidatesByPHashes(ctx context.Context, phashes [][32]byte, maxDistance, topK int) ([]*domain.Certificate, error)
}

type BlockchainService interface {
	RegisterHash(ctx context.Context, hash string) (txHash string, blockNum uint64, err error)
	IsHashRegistered(ctx context.Context, hash string) (bool, error)
}

type FeatureExtractor interface {
	Compute(ctx context.Context, content []byte) (*domain.FeatureSignature, []byte, error)
	Match(ctx context.Context, refSig, candSig *domain.FeatureSignature, refImage, candImage []byte) (domain.MatchDecision, error)
}

type ImageBlobStore interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
}
