package usecase

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/observability"
)

type CertifyUseCase struct {
	repo      CertificateRepository
	extractor FeatureExtractor
	blobs     ImageBlobStore
}

// NewCertifyUseCase builds the certify workflow. On-chain anchoring is no longer
// part of the request path: the certificate is persisted with AnchorStatus
// "pending" and the background anchor worker (internal/anchor) anchors it
// asynchronously. This removes the blockchain dependency from the synchronous
// path and fixes the previous double-anchor / orphan-anchor consistency bugs.
func NewCertifyUseCase(repo CertificateRepository, extractor FeatureExtractor, blobs ImageBlobStore) *CertifyUseCase {
	return &CertifyUseCase{repo: repo, extractor: extractor, blobs: blobs}
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
	var blobKey string
	if phash != nil {
		extractHandle := rec.StartStage(ctx, "orb_extract")
		sig, jpegBytes, ferr := uc.extractor.Compute(ctx, content)
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
				observability.Attr{Key: "jpeg_bytes", Value: len(jpegBytes)},
			)
			extractHandle.End()
			signature = sig
			blobKey = contentHash + ".jpg"
			if err = observability.StageVoid(ctx, "blob_put", func(h observability.StageHandle) error {
				h.SetAttrs(observability.Attr{Key: "blob_key", Value: blobKey})
				return uc.blobs.Put(ctx, blobKey, jpegBytes)
			}); err != nil {
				return nil, fmt.Errorf("certify: storing image blob: %w", err)
			}
		}
	}

	commitment := domain.FeatureCommitment(phash, signature)
	commitmentHex := hex.EncodeToString(commitment[:])
	observability.StageVoid(ctx, "feature_commitment", func(h observability.StageHandle) error {
		h.SetAttrs(observability.Attr{Key: "commitment", Value: commitmentHex})
		return nil
	})

	// Persist with AnchorStatus "pending" BEFORE any on-chain work. The
	// content_hash UNIQUE constraint is the real idempotency gate: of two
	// concurrent identical requests exactly one INSERT wins, so exactly one row
	// (and therefore one future anchor) survives. The anchor worker picks the row
	// up and anchors it asynchronously; a crash here leaves a retryable pending
	// row rather than spent gas with no record.
	cert := &domain.Certificate{
		ContentHash:       contentHash,
		PHash:             phash,
		Signature:         signature,
		FeatureCommitment: &commitment,
		ImageBlobKey:      blobKey,
		Registrant:        in.Registrant,
		TxHash:            "",
		BlockNumber:       0,
		AnchorStatus:      domain.AnchorPending,
		CreatedAt:         time.Now().UTC(),
	}

	if err = observability.StageVoid(ctx, "db_save", func(h observability.StageHandle) error {
		if e := uc.repo.Save(ctx, cert); e != nil {
			return e
		}
		h.SetAttrs(
			observability.Attr{Key: "cert_id", Value: cert.ID},
			observability.Attr{Key: "anchor_status", Value: cert.AnchorStatus},
		)
		return nil
	}); err != nil {
		// A concurrent request that won the UNIQUE race surfaces here as a
		// duplicate; map it to the same verdict as the up-front check.
		if errors.Is(err, domain.ErrAlreadyCertified) {
			setVerdict(observability.Verdict{Outcome: "duplicate", Detail: map[string]any{"content_hash": contentHash}})
			return nil, fmt.Errorf("certify: %w", domain.ErrAlreadyCertified)
		}
		return nil, fmt.Errorf("certify: saving certificate: %w", err)
	}

	setVerdict(observability.Verdict{Outcome: "certified", Detail: map[string]any{
		"content_hash":  contentHash,
		"anchor_status": cert.AnchorStatus,
		"cert_id":       cert.ID,
	}})
	return &CertifyOutput{Certificate: cert}, nil
}
