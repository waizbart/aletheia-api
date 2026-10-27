package usecase

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/observability"
)

// CertifyVideoUseCase turns an ingested video into a certificate.
//
// It is a sibling of CertifyUseCase rather than a branch inside it because the
// two take different inputs: an image arrives as bytes, and a video arrives as
// a handle to something already on disk with its hash already computed. Sharing
// one entry point would mean either buffering a video in memory or teaching the
// image path about spill files.
type CertifyVideoUseCase struct {
	repo   CertificateRepository
	videos VideoFeatureExtractor
}

func NewCertifyVideoUseCase(repo CertificateRepository, videos VideoFeatureExtractor) *CertifyVideoUseCase {
	return &CertifyVideoUseCase{repo: repo, videos: videos}
}

type CertifyVideoInput struct {
	// Handle is an already-ingested video. The caller owns it and closes it:
	// the signature was verified against the hash this handle produced, so the
	// ingest necessarily happened before certification was reached.
	Handle     VideoHandle
	Registrant string
	// OrgID scopes the certificate to a tenant.
	OrgID string
	// DeviceID is the attested device that captured the video.
	DeviceID string
	// CapturedAt is the device-reported capture time, covered by the device
	// signature.
	CapturedAt *time.Time
}

func (uc *CertifyVideoUseCase) Execute(ctx context.Context, in CertifyVideoInput) (out *CertifyOutput, err error) {
	rec := observability.FromContext(ctx)
	rec.SetPipeline("certify_video")

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

	if in.Handle == nil {
		return nil, fmt.Errorf("certify video: handle is required")
	}

	contentHash := in.Handle.ContentHash()
	probe := in.Handle.Probe()
	observability.StageVoid(ctx, "video_probe", func(h observability.StageHandle) error {
		h.SetAttrs(
			observability.Attr{Key: "content_hash", Value: contentHash},
			observability.Attr{Key: "size_bytes", Value: probe.SizeBytes},
			observability.Attr{Key: "duration_ms", Value: probe.DurationMs},
			observability.Attr{Key: "width", Value: probe.Width},
			observability.Attr{Key: "height", Value: probe.Height},
			observability.Attr{Key: "fps", Value: probe.FPS},
			observability.Attr{Key: "frame_count", Value: probe.FrameCount},
		)
		return nil
	})

	existing, err := observability.Stage(ctx, "duplicate_check", func(h observability.StageHandle) (*domain.Certificate, error) {
		c, e := uc.repo.FindByHash(ctx, contentHash)
		if e == nil {
			h.SetAttrs(observability.Attr{Key: "is_duplicate", Value: c != nil})
		}
		return c, e
	})
	if err != nil {
		return nil, fmt.Errorf("certify video: checking existing: %w", err)
	}
	if existing != nil {
		setVerdict(observability.Verdict{Outcome: "duplicate", Detail: map[string]any{"content_hash": contentHash}})
		return nil, fmt.Errorf("certify video: %w", domain.ErrAlreadyCertified)
	}

	reduction, rerr := observability.Stage(ctx, "video_sample", func(h observability.StageHandle) (*VideoReduction, error) {
		r, e := uc.videos.Reduce(ctx, in.Handle)
		if e == nil {
			h.SetAttrs(
				observability.Attr{Key: "frames_sampled", Value: r.Signature.FrameCount()},
				observability.Attr{Key: "anchor_index", Value: r.Signature.AnchorIndex},
				observability.Attr{Key: "anchor_keypoints", Value: r.Anchor.KeypointCount()},
				observability.Attr{Key: "anchor_descriptors", Value: r.Anchor.DescriptorCount()},
				observability.Attr{Key: "seq_entropy", Value: sequenceEntropy(r.Signature.FramePHashes)},
			)
		}
		return r, e
	})

	cert := &domain.Certificate{
		ContentHash: contentHash,
		MediaKind:   domain.MediaKindVideo,
		Registrant:  in.Registrant,
		CreatedAt:   time.Now().UTC(),
		OrgID:       in.OrgID,
		DeviceID:    in.DeviceID,
		CapturedAt:  in.CapturedAt,
	}

	var commitment [32]byte
	if rerr != nil {
		// Extraction failure is not fatal, exactly as on the image path: the
		// certificate still proves that an attested device signed these bytes,
		// and exact SHA-256 verification still works. Only the perceptual half
		// is lost.
		//
		// Nothing else about the video is stored in that case. Duration is
		// known but would not be inside the commitment, and a certificate
		// asserting uncommitted facts is the integrity hole the commitment
		// exists to close.
		log.Printf("certify video: reduction failed for %s: %v", contentHash, rerr)
		commitment = domain.VideoFeatureCommitment(nil, nil, nil)
	} else {
		anchorPHash := reduction.AnchorPHash
		cert.PHash = &anchorPHash
		cert.Signature = reduction.Anchor
		cert.Video = reduction.Signature
		cert.DurationMs = reduction.Signature.DurationMs
		commitment = domain.VideoFeatureCommitment(&anchorPHash, reduction.Anchor, reduction.Signature)
	}
	cert.FeatureCommitment = &commitment

	commitmentHex := hex.EncodeToString(commitment[:])
	observability.StageVoid(ctx, "feature_commitment", func(h observability.StageHandle) error {
		h.SetAttrs(observability.Attr{Key: "commitment", Value: commitmentHex})
		return nil
	})

	if err = observability.StageVoid(ctx, "db_save", func(h observability.StageHandle) error {
		if e := uc.repo.Save(ctx, cert); e != nil {
			return e
		}
		h.SetAttrs(observability.Attr{Key: "cert_id", Value: cert.ID})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("certify video: saving certificate: %w", err)
	}

	setVerdict(observability.Verdict{Outcome: "certified", Detail: map[string]any{
		"content_hash": contentHash,
		"commitment":   commitmentHex,
		"cert_id":      cert.ID,
		"media_kind":   string(domain.MediaKindVideo),
	}})
	return &CertifyOutput{Certificate: cert}, nil
}

// sequenceEntropy is the mean Hamming distance between consecutive sampled
// frames. It is reported, never gated: a low value means the frame sequence
// carries little information, which is the locked-off-shot case where the
// anchor match is doing all the discriminating. Surfacing it is what makes that
// situation diagnosable instead of merely surprising.
func sequenceEntropy(frames [][32]byte) float64 {
	if len(frames) < 2 {
		return 0
	}
	sum := 0
	for i := 1; i < len(frames); i++ {
		sum += domain.Hamming256(frames[i-1], frames[i])
	}
	return float64(sum) / float64(len(frames)-1)
}
