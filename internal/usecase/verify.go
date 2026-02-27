package usecase

import (
	"context"
	"fmt"
	"io"

	"github.com/waizbart/aletheia-api/internal/domain"
)

type VerifyUseCase struct {
	repo CertificateRepository
}

func NewVerifyUseCase(repo CertificateRepository) *VerifyUseCase {
	return &VerifyUseCase{repo: repo}
}

type VerifyInput struct {
	Content io.Reader
	Hash    string
}

type VerifyOutput struct {
	Certified   bool
	Certificate *domain.Certificate
}

func (uc *VerifyUseCase) Execute(ctx context.Context, in VerifyInput) (*VerifyOutput, error) {
	hash := in.Hash

	if in.Content != nil {
		computed, err := domain.HashContent(in.Content)
		if err != nil {
			return nil, fmt.Errorf("verify: %w", err)
		}
		hash = computed
	}

	if hash == "" {
		return nil, fmt.Errorf("verify: no content or hash provided")
	}

	cert, err := uc.repo.FindByHash(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}

	if cert == nil {
		return &VerifyOutput{Certified: false}, nil
	}

	return &VerifyOutput{Certified: true, Certificate: cert}, nil
}
