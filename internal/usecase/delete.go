package usecase

import (
	"context"
	"fmt"

	"github.com/waizbart/aletheia-api/internal/domain"
)

type DeleteUseCase struct {
	repo  CertificateRepository
	blobs ImageBlobStore
}

func NewDeleteUseCase(repo CertificateRepository, blobs ImageBlobStore) *DeleteUseCase {
	return &DeleteUseCase{repo: repo, blobs: blobs}
}

type DeleteInput struct {
	Hash string
}

// Execute removes a certificate and its normalized image blob. The blob is
// deleted before the database row so a failed run leaves a retryable state: the
// row still points at a (now possibly absent) blob, and DeleteObject is
// idempotent, so a retry converges. Returns domain.ErrNotFound when no
// certificate matches the hash.
func (uc *DeleteUseCase) Execute(ctx context.Context, in DeleteInput) error {
	if in.Hash == "" {
		return fmt.Errorf("delete: hash is required")
	}

	cert, err := uc.repo.FindByHash(ctx, in.Hash)
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	if cert == nil {
		return fmt.Errorf("delete: %w", domain.ErrNotFound)
	}

	if cert.ImageBlobKey != "" {
		if err := uc.blobs.Delete(ctx, cert.ImageBlobKey); err != nil {
			return fmt.Errorf("delete: removing image blob: %w", err)
		}
	}

	if err := uc.repo.Delete(ctx, in.Hash); err != nil {
		return fmt.Errorf("delete: %w", err)
	}

	return nil
}
