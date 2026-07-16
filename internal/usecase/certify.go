package usecase

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/observability"
)

type CertifyUseCase struct {
	repo      CertificateRepository
	chain     BlockchainService
	extractor FeatureExtractor
}

func NewCertifyUseCase(repo CertificateRepository, chain BlockchainService, extractor FeatureExtractor) *CertifyUseCase {
	return &CertifyUseCase{repo: repo, chain: chain, extractor: extractor}
}

type CertifyInput struct {
	Content    io.Reader
	Registrant string
}

type CertifyOutput struct {
	Certificate *domain.Certificate
}

func (uc *CertifyUseCase) Execute(ctx context.Context, in CertifyInput) (out *CertifyOutput, err error) {
	rec := observability.FromContext(ctx)
	rec.SetPipeline("certify")

	verdictSet := false
	setVerdict := func(v observability.Verdict) {
		verdictSet = true
		rec.SetVerdict(v)
	}
	defer func() {
		if !verdictSet && err != nil {
			rec.SetVerdict(observability.Verdict{Outcome: "error", Detail: map[string]any{"error": err.Error()}})
		}
	}()

	content, err := io.ReadAll(in.Content)
	if err != nil {
		return nil, fmt.Errorf("certify: hashing content: %w", err)
	}

	contentHash, _ := observability.Stage(ctx, "sha256", func(h observability.StageHandle) (string, error) {
		hash, _ := domain.HashContent(bytes.NewReader(content))
		h.SetAttrs(
			observability.Attr{Key: "content_hash", Value: hash},
			observability.Attr{Key: "size_bytes", Value: len(content)},
		)
		return hash, nil
	})

	existing, err := observability.Stage(ctx, "duplicate_check", func(h observability.StageHandle) (*domain.Certificate, error) {
		c, e := uc.repo.FindByHash(ctx, contentHash)
		if e == nil {
			h.SetAttrs(observability.Attr{Key: "is_duplicate", Value: c != nil})
		}
		return c, e
	})
	if err != nil {
		return nil, fmt.Errorf("certify: checking existing: %w", err)
	}
	if existing != nil {
		setVerdict(observability.Verdict{Outcome: "duplicate", Detail: map[string]any{"content_hash": contentHash}})
		return nil, fmt.Errorf("certify: %w", domain.ErrAlreadyCertified)
	}

	phash, _ := observability.Stage(ctx, "phash256", func(h observability.StageHandle) (*[32]byte, error) {
		p := domain.PHash256(content)
		h.SetAttrs(observability.Attr{Key: "has_phash", Value: p != nil})
		if p != nil {
			h.SetAttrs(observability.Attr{Key: "phash", Value: hex.EncodeToString(p[:])})
		}
		return p, nil
	})

	var signature *domain.FeatureSignature
	if phash != nil {
		extractHandle := rec.StartStage(ctx, "orb_extract")
		sig, ferr := uc.extractor.Compute(ctx, content)
		if ferr != nil {
			// Extraction failure is non-fatal: the certificate is still anchored
			// with a phash-only commitment. Preserve that behavior.
			extractHandle.Fail(ferr)
			extractHandle.End()
			log.Printf("certify: feature extraction failed for %s: %v", contentHash, ferr)
		} else {
			extractHandle.SetAttrs(
				observability.Attr{Key: "keypoints", Value: sig.KeypointCount()},
				observability.Attr{Key: "descriptors", Value: sig.DescriptorCount()},
				observability.Attr{Key: "color_grid_bytes", Value: len(sig.ColorGrid)},
			)
			extractHandle.End()
			signature = sig
		}
	}

	commitment := domain.FeatureCommitment(phash, signature)
	commitmentHex := hex.EncodeToString(commitment[:])
	observability.StageVoid(ctx, "feature_commitment", func(h observability.StageHandle) error {
		h.SetAttrs(observability.Attr{Key: "commitment", Value: commitmentHex})
		return nil
	})

	var txHash string
	var blockNum uint64
	if err = observability.StageVoid(ctx, "chain_anchor", func(h observability.StageHandle) error {
		tx, bn, e := uc.chain.RegisterHash(ctx, contentHash, commitmentHex)
		if e == nil {
			h.SetAttrs(
				observability.Attr{Key: "tx_hash", Value: tx},
				observability.Attr{Key: "block_number", Value: int64(bn)},
			)
		}
		txHash, blockNum = tx, bn
		return e
	}); err != nil {
		return nil, fmt.Errorf("certify: registering on chain: %w", err)
	}

	cert := &domain.Certificate{
		ContentHash:       contentHash,
		PHash:             phash,
		Signature:         signature,
		FeatureCommitment: &commitment,
		Registrant:        in.Registrant,
		TxHash:            txHash,
		BlockNumber:       blockNum,
		CreatedAt:         time.Now().UTC(),
	}

	if err = observability.StageVoid(ctx, "db_save", func(h observability.StageHandle) error {
		if e := uc.repo.Save(ctx, cert); e != nil {
			return e
		}
		h.SetAttrs(observability.Attr{Key: "cert_id", Value: cert.ID})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("certify: saving certificate: %w", err)
	}

	setVerdict(observability.Verdict{Outcome: "certified", Detail: map[string]any{
		"content_hash": contentHash,
		"tx_hash":      txHash,
		"cert_id":      cert.ID,
	}})
	return &CertifyOutput{Certificate: cert}, nil
}
