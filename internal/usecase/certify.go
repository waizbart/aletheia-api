package usecase

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
)

type CertifyUseCase struct {
	repo  CertificateRepository
	chain BlockchainService
}

func NewCertifyUseCase(repo CertificateRepository, chain BlockchainService) *CertifyUseCase {
	return &CertifyUseCase{repo: repo, chain: chain}
}

type CertifyInput struct {
	Content    io.Reader
	Registrant string
}

type CertifyOutput struct {
	Certificate *domain.Certificate
}

func (uc *CertifyUseCase) Execute(ctx context.Context, in CertifyInput) (*CertifyOutput, error) {
	content, err := io.ReadAll(in.Content)
	if err != nil {
		return nil, fmt.Errorf("certify: hashing content: %w", err)
	}

	contentHash, err := domain.HashContent(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("certify: %w", err)
	}
	perceptualHash, err := domain.PerceptualHashFromBytes(content)
	if err != nil {
		return nil, fmt.Errorf("certify: computing perceptual hash: %w", err)
	}

	existing, err := uc.repo.FindByHash(ctx, contentHash)
	if err != nil {
		return nil, fmt.Errorf("certify: checking existing: %w", err)
	}
	if existing != nil {
		return nil, fmt.Errorf("certify: %w", domain.ErrAlreadyCertified)
	}

	txHash, blockNum, err := uc.chain.RegisterHash(ctx, contentHash)
	if err != nil {
		return nil, fmt.Errorf("certify: registering on chain: %w", err)
	}

	cert := &domain.Certificate{
		ContentHash:    contentHash,
		PerceptualHash: perceptualHash,
		Registrant:     in.Registrant,
		TxHash:         txHash,
		BlockNumber:    blockNum,
		CreatedAt:      time.Now().UTC(),
	}

	if err := uc.repo.Save(ctx, cert); err != nil {
		return nil, fmt.Errorf("certify: saving certificate: %w", err)
	}

	return &CertifyOutput{Certificate: cert}, nil
}
