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
	// Compute extracts the full stored signature from an image: ORB keypoints
	// and descriptors plus the color grid (per-cell LAB means) and reference
	// dimensions the matcher's color-residual gate reads at verify time.
	Compute(ctx context.Context, content []byte) (*domain.FeatureSignature, error)
	// Match compares a stored reference signature against a candidate. Only the
	// candidate image bytes are needed — the reference side runs entirely from
	// the signature, so no reference image is stored or fetched anywhere.
	Match(ctx context.Context, refSig, candSig *domain.FeatureSignature, candImage []byte) (domain.MatchDecision, error)
}

// ColorGridRenderer renders a stored color grid back to a small PNG so the
// observability dashboard can show candidate thumbnails without any stored
// reference image.
type ColorGridRenderer interface {
	RenderColorGridPNG(grid []byte, refWidth, refHeight int) ([]byte, error)
}
