package usecase

import (
	"context"

	"github.com/waizbart/aletheia-api/internal/domain"
)

type CertificateRepository interface {
	Save(ctx context.Context, cert *domain.Certificate) error
	FindByHash(ctx context.Context, contentHash string) (*domain.Certificate, error)
	FindCandidatesByPHashes(ctx context.Context, phashes [][32]byte, maxDistance, topK int) ([]*domain.Certificate, error)
	Delete(ctx context.Context, contentHash string) error
}

type BlockchainService interface {
	// RegisterHash anchors the certificate on chain. The contentHash is the
	// SHA-256 of the original content; featureCommitment is the 32-byte digest
	// binding the off-chain feature bundle to this certificate (see
	// domain.FeatureCommitment). When the certificate has no extractable
	// features (non-image content), featureCommitment is the deterministic
	// commitment of the empty bundle.
	RegisterHash(ctx context.Context, contentHash, featureCommitment string) (txHash string, blockNum uint64, err error)
	IsHashRegistered(ctx context.Context, hash string) (bool, error)
}

type FeatureExtractor interface {
	Compute(ctx context.Context, content []byte) (*domain.FeatureSignature, []byte, error)
	Match(ctx context.Context, refSig, candSig *domain.FeatureSignature, refImage, candImage []byte) (domain.MatchDecision, error)
}

type ImageBlobStore interface {
	Put(ctx context.Context, key string, data []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}
