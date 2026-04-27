package usecase

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
)

type CertifyUseCase struct {
	repo      CertificateRepository
	chain     BlockchainService
	extractor FeatureExtractor
	blobs     ImageBlobStore
}

func NewCertifyUseCase(repo CertificateRepository, chain BlockchainService, extractor FeatureExtractor, blobs ImageBlobStore) *CertifyUseCase {
	return &CertifyUseCase{repo: repo, chain: chain, extractor: extractor, blobs: blobs}
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

	contentHash, _ := domain.HashContent(bytes.NewReader(content))

	existing, err := uc.repo.FindByHash(ctx, contentHash)
	if err != nil {
		return nil, fmt.Errorf("certify: checking existing: %w", err)
	}
	if existing != nil {
		return nil, fmt.Errorf("certify: %w", domain.ErrAlreadyCertified)
	}

	phash := domain.PHash256(content)

	var signature *domain.FeatureSignature
	var blobKey string
	if phash != nil {
		sig, jpegBytes, ferr := uc.extractor.Compute(ctx, content)
		if ferr != nil {
			log.Printf("certify: feature extraction failed for %s: %v", contentHash, ferr)
		} else {
			signature = sig
			blobKey = contentHash + ".jpg"
			if err := uc.blobs.Put(ctx, blobKey, jpegBytes); err != nil {
				return nil, fmt.Errorf("certify: storing image blob: %w", err)
			}
		}
	}

	txHash, blockNum, err := uc.chain.RegisterHash(ctx, contentHash)
	if err != nil {
		return nil, fmt.Errorf("certify: registering on chain: %w", err)
	}

	cert := &domain.Certificate{
		ContentHash:  contentHash,
		PHash:        phash,
		Signature:    signature,
		ImageBlobKey: blobKey,
		Registrant:   in.Registrant,
		TxHash:       txHash,
		BlockNumber:  blockNum,
		CreatedAt:    time.Now().UTC(),
	}

	if err := uc.repo.Save(ctx, cert); err != nil {
		return nil, fmt.Errorf("certify: saving certificate: %w", err)
	}

	return &CertifyOutput{Certificate: cert}, nil
}
