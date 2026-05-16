package usecase

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"

	"github.com/waizbart/aletheia-api/internal/domain"
)

const verifyTopK = 20

type VerifyUseCase struct {
	repo      CertificateRepository
	extractor FeatureExtractor
	blobs     ImageBlobStore
}

func NewVerifyUseCase(repo CertificateRepository, extractor FeatureExtractor, blobs ImageBlobStore) *VerifyUseCase {
	return &VerifyUseCase{repo: repo, extractor: extractor, blobs: blobs}
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
	if in.Content == nil && in.Hash == "" {
		return nil, fmt.Errorf("verify: no content or hash provided")
	}

	if in.Content == nil {
		cert, err := uc.repo.FindByHash(ctx, in.Hash)
		if err != nil {
			return nil, fmt.Errorf("verify: %w", err)
		}
		if cert == nil {
			return &VerifyOutput{Certified: false}, nil
		}
		return &VerifyOutput{Certified: true, Certificate: cert}, nil
	}

	content, err := io.ReadAll(in.Content)
	if err != nil {
		return nil, fmt.Errorf("verify: hashing content: %w", err)
	}

	contentHash, _ := domain.HashContent(bytes.NewReader(content))
	cert, err := uc.repo.FindByHash(ctx, contentHash)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	if cert != nil {
		return &VerifyOutput{Certified: true, Certificate: cert}, nil
	}

	phashes := domain.PHash256Variants(content)
	if len(phashes) == 0 {
		return &VerifyOutput{Certified: false}, nil
	}

	candSig, _, err := uc.extractor.Compute(ctx, content)
	if err != nil {
		log.Printf("verify: feature extraction failed: %v", err)
		return &VerifyOutput{Certified: false}, nil
	}

	candidates, err := uc.repo.FindCandidatesByPHashes(ctx, phashes, domain.MaxPHashDistance, verifyTopK)
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}

	for _, c := range candidates {
		if c.Signature == nil || c.ImageBlobKey == "" {
			continue
		}
		refImage, err := uc.blobs.Get(ctx, c.ImageBlobKey)
		if err != nil {
			log.Printf("verify: fetch blob %s: %v", c.ImageBlobKey, err)
			continue
		}
		decision, err := uc.extractor.Match(ctx, c.Signature, candSig, refImage, content)
		if err != nil {
			log.Printf("verify: match against %s: %v", c.ID, err)
			continue
		}
		if decision.Matched {
			return &VerifyOutput{Certified: true, Certificate: c}, nil
		}
	}

	return &VerifyOutput{Certified: false}, nil
}
