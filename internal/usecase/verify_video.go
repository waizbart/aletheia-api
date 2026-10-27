package usecase

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/observability"
)

// videoVerifyTopK bounds how many prefilter candidates advance to matching.
//
// It is lower than verifyTopK because a video candidate costs far more: the
// cheap gates are free, but a survivor pays a second decode pass for the anchor
// frame plus a full ORB extraction and match. The media-kind partition already
// sharpens the candidate set, so a narrower window loses little.
const videoVerifyTopK = 32

// VerifyVideoUseCase answers whether an uploaded video is certified.
//
// It mirrors VerifyUseCase: exact SHA-256 first, then a perceptual path so the
// same video re-encoded still matches. There is deliberately no excerpt
// semantics — a cut of a certified video is expected to fail — which is what
// lets the frame sequence be compared slot for slot with no temporal alignment.
type VerifyVideoUseCase struct {
	repo      CertificateRepository
	videos    VideoFeatureExtractor
	extractor FeatureExtractor
}

func NewVerifyVideoUseCase(repo CertificateRepository, videos VideoFeatureExtractor, extractor FeatureExtractor) *VerifyVideoUseCase {
	return &VerifyVideoUseCase{repo: repo, videos: videos, extractor: extractor}
}

type VerifyVideoInput struct {
	Content io.Reader
}

func (uc *VerifyVideoUseCase) Execute(ctx context.Context, in VerifyVideoInput) (out *VerifyOutput, err error) {
	rec := observability.FromContext(ctx)
	rec.SetPipeline("verify_video")

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

	if in.Content == nil {
		return nil, fmt.Errorf("verify video: no content provided")
	}

	handle, err := observability.Stage(ctx, "video_ingest", func(h observability.StageHandle) (VideoHandle, error) {
		src, e := uc.videos.Ingest(ctx, in.Content)
		if e == nil {
			h.SetAttrs(
				observability.Attr{Key: "content_hash", Value: src.ContentHash()},
				observability.Attr{Key: "size_bytes", Value: src.Probe().SizeBytes},
			)
		}
		return src, e
	})
	if err != nil {
		return nil, fmt.Errorf("verify video: %w", err)
	}
	defer func() {
		if cerr := handle.Close(); cerr != nil {
			log.Printf("verify video: releasing handle: %v", cerr)
		}
	}()

	probe := handle.Probe()
	if err = observability.StageVoid(ctx, "video_probe", func(h observability.StageHandle) error {
		h.SetAttrs(
			observability.Attr{Key: "duration_ms", Value: probe.DurationMs},
			observability.Attr{Key: "width", Value: probe.Width},
			observability.Attr{Key: "height", Value: probe.Height},
			observability.Attr{Key: "fps", Value: probe.FPS},
			observability.Attr{Key: "frame_count", Value: probe.FrameCount},
		)
		return probe.Validate()
	}); err != nil {
		return nil, fmt.Errorf("verify video: %w", err)
	}

	contentHash := handle.ContentHash()
	cert, err := observability.Stage(ctx, "exact_lookup", func(h observability.StageHandle) (*domain.Certificate, error) {
		c, e := uc.repo.FindByHash(ctx, contentHash)
		if e == nil {
			h.SetAttrs(observability.Attr{Key: "is_match", Value: c != nil})
		}
		return c, e
	})
	if err != nil {
		return nil, fmt.Errorf("verify video: %w", err)
	}
	if cert != nil {
		setVerdict(observability.Verdict{Outcome: "match", Detail: map[string]any{"cert_id": cert.ID, "via": "sha256"}})
		return &VerifyOutput{Certified: true, Certificate: cert}, nil
	}

	reduction, rerr := observability.Stage(ctx, "video_sample", func(h observability.StageHandle) (*VideoReduction, error) {
		r, e := uc.videos.Reduce(ctx, handle)
		if e == nil {
			h.SetAttrs(
				observability.Attr{Key: "frames_sampled", Value: r.Signature.FrameCount()},
				observability.Attr{Key: "anchor_index", Value: r.Signature.AnchorIndex},
				observability.Attr{Key: "seq_entropy", Value: sequenceEntropy(r.Signature.FramePHashes)},
			)
		}
		return r, e
	})
	if rerr != nil {
		log.Printf("verify video: reduction failed: %v", rerr)
		setVerdict(observability.Verdict{Outcome: "no_match", Detail: map[string]any{"reason": "falha na extração de features do vídeo"}})
		return &VerifyOutput{Certified: false}, nil
	}

	candidates, err := observability.Stage(ctx, "lsh_prefilter", func(h observability.StageHandle) ([]*domain.Certificate, error) {
		// Every sampled frame probes the index, not just the anchor. The
		// prefilter does not need to know which slot the reference chose — it
		// only needs one of this candidate's frames to land near the stored
		// anchor hash, which dissolves the circularity of needing the
		// certificate in order to know which frame to look the certificate up
		// with.
		c, e := uc.repo.FindCandidatesByPHashes(ctx, reduction.Signature.FramePHashes,
			domain.MediaKindVideo, domain.MaxPHashDistance, videoVerifyTopK)
		if e == nil {
			h.SetAttrs(observability.Attr{Key: "candidates", Value: len(c)})
		}
		return c, e
	})
	if err != nil {
		return nil, fmt.Errorf("verify video: %w", err)
	}

	matchStage := rec.StartStage(ctx, "candidate_matching")
	matchStage.SetAttrs(observability.Attr{Key: "candidates", Value: len(candidates)})
	for _, c := range candidates {
		child := matchStage.Child("candidate")
		child.SetAttrs(observability.Attr{Key: "cert_id", Value: c.ID})

		if !c.Video.Valid() || !c.Signature.HasColorGrid() {
			child.Skip("candidato sem sequência de quadros ou grade de cores")
			child.End()
			continue
		}

		// The cheap gates run first. Each one that rejects saves a decode pass
		// and an ORB extraction.
		durationOK := domain.DurationWithinTolerance(c.DurationMs, reduction.Signature.DurationMs)
		agreement, matched := domain.FrameSequenceScore(
			c.Video.FramePHashes, reduction.Signature.FramePHashes)
		child.SetAttrs(
			observability.Attr{Key: "ref_duration_ms", Value: c.DurationMs},
			observability.Attr{Key: "cand_duration_ms", Value: reduction.Signature.DurationMs},
			observability.Attr{Key: "duration_ok", Value: durationOK},
			observability.Attr{Key: "agreement", Value: agreement},
			observability.Attr{Key: "frames_matched", Value: matched},
			observability.Attr{Key: "min_frame_agreement", Value: domain.MinFrameAgreement},
		)
		if !durationOK || agreement < domain.MinFrameAgreement {
			child.SetAttrs(observability.Attr{Key: "reason", Value: videoRejectReason(durationOK, agreement)})
			child.End()
			continue
		}

		// The slot to compare is the reference's, read off the certificate.
		// This candidate may well have ranked its own frames differently.
		frame, ferr := uc.videos.FrameAt(ctx, handle, c.Video.AnchorIndex)
		if ferr != nil {
			log.Printf("verify video: anchor frame for %s: %v", c.ID, ferr)
			child.Fail(ferr)
			child.End()
			continue
		}

		candSig, cerr := uc.extractor.Compute(ctx, frame)
		if cerr != nil {
			log.Printf("verify video: anchor features for %s: %v", c.ID, cerr)
			child.Fail(cerr)
			child.End()
			continue
		}

		decision, merr := uc.extractor.Match(ctx, c.Signature, candSig, frame)
		if merr != nil {
			log.Printf("verify video: anchor match for %s: %v", c.ID, merr)
			child.Fail(merr)
			child.End()
			continue
		}

		child.SetAttrs(
			observability.Attr{Key: "inliers", Value: decision.Inliers},
			observability.Attr{Key: "min_inliers", Value: domain.MinInliers},
			observability.Attr{Key: "color_mean", Value: decision.ColorMean},
			observability.Attr{Key: "color_max", Value: decision.ColorMax},
			observability.Attr{Key: "coverage", Value: decision.Coverage},
			observability.Attr{Key: "anchor_matched", Value: decision.Matched},
			observability.Attr{Key: "reason", Value: matchReason(decision)},
		)
		child.End()

		if domain.DecideVideo(decision, agreement, durationOK) {
			matchStage.End()
			setVerdict(observability.Verdict{Outcome: "match", Detail: map[string]any{
				"cert_id":   c.ID,
				"via":       "similaridade de vídeo",
				"agreement": agreement,
			}})
			return &VerifyOutput{Certified: true, Certificate: c}, nil
		}
	}
	matchStage.End()

	setVerdict(observability.Verdict{Outcome: "no_match"})
	return &VerifyOutput{Certified: false}, nil
}

// videoRejectReason explains, in PT-BR, which cheap gate turned a candidate
// away before the anchor match was attempted.
func videoRejectReason(durationOK bool, agreement float64) string {
	if !durationOK {
		return "duração fora da tolerância"
	}
	return fmt.Sprintf("concordância de quadros %.2f < %.2f", agreement, domain.MinFrameAgreement)
}
