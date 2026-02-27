package usecase

import (
	"context"

	"github.com/waizbart/aletheia-api/internal/domain"
)

type CertificateRepository interface {
	Save(ctx context.Context, cert *domain.Certificate) error
	FindByHash(ctx context.Context, contentHash string) (*domain.Certificate, error)
}

type BlockchainService interface {
	RegisterHash(ctx context.Context, hash string) (txHash string, blockNum uint64, err error)
	IsHashRegistered(ctx context.Context, hash string) (bool, error)
}
