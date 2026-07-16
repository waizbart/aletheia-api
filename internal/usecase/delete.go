package usecase

import (
	"context"
	"fmt"
)

type DeleteUseCase struct {
	repo CertificateRepository
}

func NewDeleteUseCase(repo CertificateRepository) *DeleteUseCase {
	return &DeleteUseCase{repo: repo}
}

type DeleteInput struct {
	Hash string
}

// Execute removes a certificate. All certificate data (signature, color grid,
// phash bands) lives in the database, so deletion is a single atomic operation.
// Returns domain.ErrNotFound when no certificate matches the hash.
func (uc *DeleteUseCase) Execute(ctx context.Context, in DeleteInput) error {
	if in.Hash == "" {
		return fmt.Errorf("delete: hash is required")
	}

	if err := uc.repo.Delete(ctx, in.Hash); err != nil {
		return fmt.Errorf("delete: %w", err)
	}

	return nil
}
