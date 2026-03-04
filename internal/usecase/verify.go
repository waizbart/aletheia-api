package usecase

import (
	"bytes"
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
	var perceptualHash *uint64

	if in.Content != nil {
		content, err := io.ReadAll(in.Content)
		if err != nil {
			return nil, fmt.Errorf("verify: hashing content: %w", err)
		}

		computed, err := domain.HashContent(bytes.NewReader(content))
		if err != nil {
			return nil, fmt.Errorf("verify: %w", err)
		}
		hash = computed

		perceptualHash, err = domain.PerceptualHashFromBytes(content)
		if err != nil {
			return nil, fmt.Errorf("verify: computing perceptual hash: %w", err)
		}
	}

	if hash == "" {
		return nil, fmt.Errorf("verify: no content or hash provided")
	}

	cert, err := uc.repo.FindByHash(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}

	if cert == nil {
		if perceptualHash != nil {
			cert, err = uc.repo.FindByPerceptualHash(ctx, *perceptualHash, 8)
			if err != nil {
				return nil, fmt.Errorf("verify: %w", err)
			}
		}
		if cert == nil {
			return &VerifyOutput{Certified: false}, nil
		}
	}

	return &VerifyOutput{Certified: true, Certificate: cert}, nil
}
