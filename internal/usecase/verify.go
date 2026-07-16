package usecase

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/observability"
)

// verifyTopK bounds how many pHash-prefilter candidates advance to the
// (expensive) ORB+RANSAC match. Each negative query that finds no match pays a
// full match against every candidate, so this value dominates verify latency.
// Widening it (e.g. 256) was measured to quadruple the matrix-eval runtime for
// no clear recall gain: the dominant false-negative cause for geometric edits is
// pHash drift beyond MaxPHashDistance (the correct certificate is filtered out
// entirely, not merely ranked past the window), which a larger topK cannot
// recover. Prefilter recall is tracked separately; see docs/DATASET_GENERATION.md.
const verifyTopK = 64

type VerifyUseCase struct {
	repo      CertificateRepository
	extractor FeatureExtractor
}

func NewVerifyUseCase(repo CertificateRepository, extractor FeatureExtractor) *VerifyUseCase {
	return &VerifyUseCase{repo: repo, extractor: extractor}
}

type VerifyInput struct {
	Content io.Reader
	Hash    string
}

type VerifyOutput struct {
	Certified   bool
	Certificate *domain.Certificate
}

func (uc *VerifyUseCase) Execute(ctx context.Context, in VerifyInput) (out *VerifyOutput, err error) {
	rec := observability.FromContext(ctx)
	rec.SetPipeline("verify")

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

	if in.Content == nil && in.Hash == "" {
		return nil, fmt.Errorf("verify: no content or hash provided")
	}

	// Path A: verify by hash only.
	if in.Content == nil {
		cert, lerr := observability.Stage(ctx, "exact_lookup", func(h observability.StageHandle) (*domain.Certificate, error) {
			c, e := uc.repo.FindByHash(ctx, in.Hash)
			if e == nil {
				h.SetAttrs(observability.Attr{Key: "is_match", Value: c != nil})
			}
			return c, e
		})
		if lerr != nil {
			return nil, fmt.Errorf("verify: %w", lerr)
		}
		if cert == nil {
			setVerdict(observability.Verdict{Outcome: "no_match"})
			return &VerifyOutput{Certified: false}, nil
		}
		setVerdict(observability.Verdict{Outcome: "match", Detail: map[string]any{"cert_id": cert.ID}})
		return &VerifyOutput{Certified: true, Certificate: cert}, nil
	}

	// Path B: verify by uploaded image.
	content, err := io.ReadAll(in.Content)
	if err != nil {
		return nil, fmt.Errorf("verify: hashing content: %w", err)
	}

	contentHash, _ := observability.Stage(ctx, "sha256", func(h observability.StageHandle) (string, error) {
		hash, _ := domain.HashContent(bytes.NewReader(content))
		h.SetAttrs(
			observability.Attr{Key: "content_hash", Value: hash},
			observability.Attr{Key: "size_bytes", Value: len(content)},
		)
		return hash, nil
	})

	cert, err := observability.Stage(ctx, "exact_lookup", func(h observability.StageHandle) (*domain.Certificate, error) {
		c, e := uc.repo.FindByHash(ctx, contentHash)
		if e == nil {
			h.SetAttrs(observability.Attr{Key: "is_match", Value: c != nil})
		}
		return c, e
	})
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}
	if cert != nil {
		setVerdict(observability.Verdict{Outcome: "match", Detail: map[string]any{"cert_id": cert.ID, "via": "sha256"}})
		return &VerifyOutput{Certified: true, Certificate: cert}, nil
	}

	phashes, _ := observability.Stage(ctx, "phash_variants", func(h observability.StageHandle) ([][32]byte, error) {
		p := domain.PHash256Variants(content)
		h.SetAttrs(observability.Attr{Key: "variants", Value: len(p)})
		return p, nil
	})
	if len(phashes) == 0 {
		setVerdict(observability.Verdict{Outcome: "no_match", Detail: map[string]any{"reason": "imagem não decodificável"}})
		return &VerifyOutput{Certified: false}, nil
	}

	candSig, err := observability.Stage(ctx, "orb_extract", func(h observability.StageHandle) (*domain.FeatureSignature, error) {
		sig, e := uc.extractor.Compute(ctx, content)
		if e == nil {
			h.SetAttrs(
				observability.Attr{Key: "keypoints", Value: sig.KeypointCount()},
				observability.Attr{Key: "descriptors", Value: sig.DescriptorCount()},
			)
		}
		return sig, e
	})
	if err != nil {
		log.Printf("verify: feature extraction failed: %v", err)
		setVerdict(observability.Verdict{Outcome: "no_match", Detail: map[string]any{"reason": "falha na extração de features"}})
		return &VerifyOutput{Certified: false}, nil
	}

	candidates, err := observability.Stage(ctx, "lsh_prefilter", func(h observability.StageHandle) ([]*domain.Certificate, error) {
		c, e := uc.repo.FindCandidatesByPHashes(ctx, phashes, domain.MaxPHashDistance, verifyTopK)
		if e == nil {
			h.SetAttrs(observability.Attr{Key: "candidates", Value: len(c)})
		}
		return c, e
	})
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}

	matchStage := rec.StartStage(ctx, "candidate_matching")
	matchStage.SetAttrs(observability.Attr{Key: "candidates", Value: len(candidates)})
	for _, c := range candidates {
		if !c.Signature.HasColorGrid() {
			ch := matchStage.Child("candidate")
			ch.Skip("candidato sem assinatura ou grade de cores")
			ch.End()
			continue
		}

		ch := matchStage.Child("candidate")
		ch.SetAttrs(
			observability.Attr{Key: "cert_id", Value: c.ID},
			observability.Attr{Key: "thumb_hash", Value: c.ContentHash},
		)
		if c.PHash != nil {
			ch.SetAttrs(observability.Attr{Key: "hamming", Value: minHamming(phashes, *c.PHash)})
		}

		decision, merr := uc.extractor.Match(ctx, c.Signature, candSig, content)
		if merr != nil {
			log.Printf("verify: match against %s: %v", c.ID, merr)
			ch.Fail(merr)
			ch.End()
			continue
		}

		ch.SetAttrs(
			observability.Attr{Key: "inliers", Value: decision.Inliers},
			observability.Attr{Key: "min_inliers", Value: domain.MinInliers},
			observability.Attr{Key: "color_mean", Value: decision.ColorMean},
			observability.Attr{Key: "max_color_mean", Value: domain.MaxColorMean},
			observability.Attr{Key: "color_max", Value: decision.ColorMax},
			observability.Attr{Key: "max_cell_dist", Value: domain.MaxCellDist},
			observability.Attr{Key: "cells", Value: decision.Cells},
			observability.Attr{Key: "coverage", Value: decision.Coverage},
			observability.Attr{Key: "min_area_coverage", Value: domain.MinAreaCoverage},
			observability.Attr{Key: "matched", Value: decision.Matched},
			observability.Attr{Key: "reason", Value: matchReason(decision)},
		)
		ch.End()

		if decision.Matched {
			matchStage.End()
			setVerdict(observability.Verdict{Outcome: "match", Detail: map[string]any{"cert_id": c.ID, "via": "similaridade visual"}})
			return &VerifyOutput{Certified: true, Certificate: c}, nil
		}
	}
	matchStage.End()

	setVerdict(observability.Verdict{Outcome: "no_match"})
	return &VerifyOutput{Certified: false}, nil
}

// minHamming mirrors the repository's candidate scoring: the smallest Hamming
// distance between the stored phash and any of the uploaded rotation variants.
func minHamming(variants [][32]byte, phash [32]byte) int {
	minDist := domain.Hamming256(variants[0], phash)
	for _, v := range variants[1:] {
		if d := domain.Hamming256(v, phash); d < minDist {
			minDist = d
		}
	}
	return minDist
}

// matchReason explains, in PT-BR, why a candidate matched or which threshold it
// failed, mirroring domain.Decide.
func matchReason(d domain.MatchDecision) string {
	switch {
	case d.Inliers < domain.MinInliers:
		return fmt.Sprintf("inliers %d < %d", d.Inliers, domain.MinInliers)
	case d.ColorMean > domain.MaxColorMean:
		return fmt.Sprintf("cor média %.2f > %.1f", d.ColorMean, domain.MaxColorMean)
	case d.ColorMax > domain.MaxCellDist:
		return fmt.Sprintf("cor de célula %.1f > %.1f", d.ColorMax, domain.MaxCellDist)
	case d.Coverage < domain.MinAreaCoverage:
		return fmt.Sprintf("cobertura %.2f < %.2f", d.Coverage, domain.MinAreaCoverage)
	default:
		return "passou todos os limiares"
	}
}
