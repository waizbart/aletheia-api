package usecase

import (
	"context"
	"fmt"

	"github.com/waizbart/aletheia-api/internal/domain"
)

// ThumbnailUseCase renders a certificate's stored color grid as a small PNG.
// It backs the observability dashboard's candidate previews: with no reference
// image stored anywhere, the 128×128 grid of LAB cell means is the only visual
// representation of a certified image the system retains.
type ThumbnailUseCase struct {
	repo     CertificateRepository
	renderer ColorGridRenderer
}

func NewThumbnailUseCase(repo CertificateRepository, renderer ColorGridRenderer) *ThumbnailUseCase {
	return &ThumbnailUseCase{repo: repo, renderer: renderer}
}

// Execute returns a PNG thumbnail for the certificate with the given content
// hash. Returns domain.ErrNotFound when the certificate does not exist or has
// no color grid to render (non-image content, or a legacy row not yet
// backfilled).
func (uc *ThumbnailUseCase) Execute(ctx context.Context, contentHash string) ([]byte, error) {
	cert, err := uc.repo.FindByHash(ctx, contentHash)
	if err != nil {
		return nil, fmt.Errorf("thumbnail: %w", err)
	}
	if cert == nil || !cert.Signature.HasColorGrid() {
		return nil, fmt.Errorf("thumbnail: %w", domain.ErrNotFound)
	}

	png, err := uc.renderer.RenderColorGridPNG(cert.Signature.ColorGrid, cert.Signature.RefWidth, cert.Signature.RefHeight)
	if err != nil {
		return nil, fmt.Errorf("thumbnail: rendering grid: %w", err)
	}
	return png, nil
}
