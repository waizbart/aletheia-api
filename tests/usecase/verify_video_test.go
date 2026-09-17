package usecase_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

// storedVideoCert builds the reference side: a video certificate as the
// repository would hand it back.
func storedVideoCert(frames [][32]byte, anchorIdx, durationMs int) *domain.Certificate {
	anchor := frames[anchorIdx]
	return &domain.Certificate{
		ID:          "cert-video-1",
		ContentHash: "stored-video-hash",
		MediaKind:   domain.MediaKindVideo,
		DurationMs:  durationMs,
		PHash:       &anchor,
		Signature:   signatureWithGrid(),
		Video: &domain.VideoSignature{
			FramePHashes: frames,
			AnchorIndex:  anchorIdx,
			DurationMs:   durationMs,
		},
	}
}

func videoContent() io.Reader { return bytes.NewReader([]byte("video bytes")) }

// matchingExtractor accepts the anchor frame outright, so a test can isolate
// the video-specific gates from the image matcher's own behaviour.
func matchingExtractor() *mockExtractor {
	return &mockExtractor{
		computeFn: func(_ context.Context, _ []byte) (*domain.FeatureSignature, error) {
			return signatureWithGrid(), nil
		},
		matchFn: func(_ context.Context, _, _ *domain.FeatureSignature, _ []byte) (domain.MatchDecision, error) {
			return domain.MatchDecision{Matched: true, Inliers: 40, Coverage: 0.9}, nil
		},
	}
}

func candidateRepo(certs ...*domain.Certificate) *mockRepo {
	return &mockRepo{
		findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) { return nil, nil },
		findCandidatesByPHashesFn: func(_ context.Context, _ [][32]byte, kind domain.MediaKind, _, _ int) ([]*domain.Certificate, error) {
			if kind != domain.MediaKindVideo {
				return nil, errors.New("prefilter was not scoped to video")
			}
			return certs, nil
		},
	}
}

func TestVerifyVideoUseCase_RejectsMissingContent(t *testing.T) {
	uc := usecase.NewVerifyVideoUseCase(&mockRepo{}, &mockVideoExtractor{}, &mockExtractor{})
	if _, err := uc.Execute(context.Background(), usecase.VerifyVideoInput{}); err == nil {
		t.Fatal("expected an error when no content was provided")
	}
}

func TestVerifyVideoUseCase_SurfacesIngestAndProbeFailures(t *testing.T) {
	tests := []struct {
		name    string
		videos  *mockVideoExtractor
		wantErr string
	}{
		{
			name: "ingest refuses an oversized upload",
			videos: &mockVideoExtractor{
				ingestFn: func(_ context.Context, _ io.Reader) (usecase.VideoHandle, error) {
					return nil, domain.ErrVideoTooLarge
				},
			},
			wantErr: "upload size limit",
		},
		{
			name: "probe refuses an overlong video",
			videos: &mockVideoExtractor{
				ingestFn: func(_ context.Context, _ io.Reader) (usecase.VideoHandle, error) {
					return &mockVideoHandle{hash: "h", probe: domain.VideoProbe{
						DurationMs: domain.MaxVideoDurationMs + 1, Width: 640, Height: 480,
					}}, nil
				},
			},
			wantErr: "duration limit",
		},
		{
			name: "probe refuses an undecodable container",
			videos: &mockVideoExtractor{
				ingestFn: func(_ context.Context, _ io.Reader) (usecase.VideoHandle, error) {
					return &mockVideoHandle{hash: "h", probe: domain.VideoProbe{}}, nil
				},
			},
			wantErr: "could not be decoded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc := usecase.NewVerifyVideoUseCase(&mockRepo{}, tt.videos, &mockExtractor{})
			_, err := uc.Execute(context.Background(), usecase.VerifyVideoInput{Content: videoContent()})
			if err == nil {
				t.Fatalf("expected an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestVerifyVideoUseCase_ExactHashMatch(t *testing.T) {
	want := &domain.Certificate{ID: "exact", MediaKind: domain.MediaKindVideo}
	repo := &mockRepo{
		findByHashFn: func(_ context.Context, hash string) (*domain.Certificate, error) {
			if hash != "video-hash" {
				t.Errorf("looked up %q, want video-hash", hash)
			}
			return want, nil
		},
	}
	// The exact path must not decode anything: that is the whole point of
	// trying it first.
	videos := &mockVideoExtractor{
		reduceFn: func(_ context.Context, _ usecase.VideoHandle) (*usecase.VideoReduction, error) {
			t.Fatal("Reduce ran even though the hash matched exactly")
			return nil, nil
		},
	}

	uc := usecase.NewVerifyVideoUseCase(repo, videos, &mockExtractor{})
	out, err := uc.Execute(context.Background(), usecase.VerifyVideoInput{Content: videoContent()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.Certified || out.Certificate != want {
		t.Fatalf("out = %+v, want the exact certificate", out)
	}
}

func TestVerifyVideoUseCase_SurfacesRepositoryFailures(t *testing.T) {
	t.Run("exact lookup", func(t *testing.T) {
		repo := &mockRepo{findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
			return nil, errors.New("db down")
		}}
		uc := usecase.NewVerifyVideoUseCase(repo, &mockVideoExtractor{}, &mockExtractor{})
		if _, err := uc.Execute(context.Background(), usecase.VerifyVideoInput{Content: videoContent()}); err == nil {
			t.Fatal("expected the lookup failure to surface")
		}
	})

	t.Run("prefilter", func(t *testing.T) {
		repo := &mockRepo{
			findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) { return nil, nil },
			findCandidatesByPHashesFn: func(_ context.Context, _ [][32]byte, _ domain.MediaKind, _, _ int) ([]*domain.Certificate, error) {
				return nil, errors.New("db down")
			},
		}
		uc := usecase.NewVerifyVideoUseCase(repo, &mockVideoExtractor{}, &mockExtractor{})
		if _, err := uc.Execute(context.Background(), usecase.VerifyVideoInput{Content: videoContent()}); err == nil {
			t.Fatal("expected the prefilter failure to surface")
		}
	})
}

// A video the decoder cannot reduce is reported as uncertified rather than as
// an error, matching the image path: the caller asked a question and the honest
// answer is "no match", not "the server broke".
func TestVerifyVideoUseCase_ReductionFailureIsNotAnError(t *testing.T) {
	repo := &mockRepo{findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
		return nil, nil
	}}
	videos := &mockVideoExtractor{
		reduceFn: func(_ context.Context, _ usecase.VideoHandle) (*usecase.VideoReduction, error) {
			return nil, errors.New("no frames")
		},
	}

	uc := usecase.NewVerifyVideoUseCase(repo, videos, &mockExtractor{})
	out, err := uc.Execute(context.Background(), usecase.VerifyVideoInput{Content: videoContent()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Certified {
		t.Error("an unreducible video was reported as certified")
	}
}

func TestVerifyVideoUseCase_CandidateGates(t *testing.T) {
	frames := videoFrames(1, domain.VideoFrameSamples)
	unrelated := videoFrames(9, domain.VideoFrameSamples)

	tests := []struct {
		name      string
		candidate *domain.Certificate
		extractor *mockExtractor
		videos    *mockVideoExtractor
		wantMatch bool
	}{
		{
			name:      "every gate passes",
			candidate: storedVideoCert(frames, 4, 8_000),
			extractor: matchingExtractor(),
			wantMatch: true,
		},
		{
			name: "candidate with no frame sequence is skipped",
			candidate: &domain.Certificate{
				ID: "legacy", MediaKind: domain.MediaKindVideo, Signature: signatureWithGrid(),
			},
			extractor: matchingExtractor(),
		},
		{
			name: "candidate with no color grid is skipped",
			candidate: &domain.Certificate{
				ID: "gridless", MediaKind: domain.MediaKindVideo, DurationMs: 8_000,
				Video: &domain.VideoSignature{FramePHashes: frames, AnchorIndex: 4, DurationMs: 8_000},
			},
			extractor: matchingExtractor(),
		},
		{
			name:      "duration out of tolerance is rejected before any decode",
			candidate: storedVideoCert(frames, 4, 60_000),
			extractor: matchingExtractor(),
		},
		{
			name:      "frame sequence below the agreement gate is rejected",
			candidate: storedVideoCert(unrelated, 4, 8_000),
			extractor: matchingExtractor(),
		},
		{
			name:      "anchor frame that fails the image gates is rejected",
			candidate: storedVideoCert(frames, 4, 8_000),
			extractor: &mockExtractor{
				computeFn: func(_ context.Context, _ []byte) (*domain.FeatureSignature, error) {
					return signatureWithGrid(), nil
				},
				matchFn: func(_ context.Context, _, _ *domain.FeatureSignature, _ []byte) (domain.MatchDecision, error) {
					return domain.MatchDecision{Matched: false, Inliers: 3}, nil
				},
			},
		},
		{
			name:      "anchor frame that will not decode is skipped",
			candidate: storedVideoCert(frames, 4, 8_000),
			extractor: matchingExtractor(),
			videos: &mockVideoExtractor{
				frameAtFn: func(_ context.Context, _ usecase.VideoHandle, _ int) ([]byte, error) {
					return nil, errors.New("seek failed")
				},
			},
		},
		{
			name:      "anchor frame whose features cannot be computed is skipped",
			candidate: storedVideoCert(frames, 4, 8_000),
			extractor: &mockExtractor{
				computeFn: func(_ context.Context, _ []byte) (*domain.FeatureSignature, error) {
					return nil, errors.New("too small")
				},
			},
		},
		{
			name:      "anchor match failure is skipped",
			candidate: storedVideoCert(frames, 4, 8_000),
			extractor: &mockExtractor{
				computeFn: func(_ context.Context, _ []byte) (*domain.FeatureSignature, error) {
					return signatureWithGrid(), nil
				},
				matchFn: func(_ context.Context, _, _ *domain.FeatureSignature, _ []byte) (domain.MatchDecision, error) {
					return domain.MatchDecision{}, errors.New("homography failed")
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			videos := tt.videos
			if videos == nil {
				videos = &mockVideoExtractor{}
			}

			uc := usecase.NewVerifyVideoUseCase(candidateRepo(tt.candidate), videos, tt.extractor)
			out, err := uc.Execute(context.Background(), usecase.VerifyVideoInput{Content: videoContent()})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Certified != tt.wantMatch {
				t.Errorf("Certified = %v, want %v", out.Certified, tt.wantMatch)
			}
		})
	}
}

// The slot compared must be the one recorded on the certificate, not the one
// this candidate would have chosen for itself. Sharpness ranking is a pure
// function of the frames, and a re-encode changes the frames.
func TestVerifyVideoUseCase_ComparesTheReferenceAnchorSlot(t *testing.T) {
	frames := videoFrames(1, domain.VideoFrameSamples)
	const referenceAnchor = 11

	reduction := validReduction()
	reduction.Signature.FramePHashes = frames
	reduction.Signature.AnchorIndex = 2 // what the candidate picked for itself

	videos := &mockVideoExtractor{
		reduceFn: func(_ context.Context, _ usecase.VideoHandle) (*usecase.VideoReduction, error) {
			return reduction, nil
		},
	}

	uc := usecase.NewVerifyVideoUseCase(
		candidateRepo(storedVideoCert(frames, referenceAnchor, 8_000)), videos, matchingExtractor())

	out, err := uc.Execute(context.Background(), usecase.VerifyVideoInput{Content: videoContent()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.Certified {
		t.Fatal("expected a match")
	}
	if len(videos.frameAtSlots) != 1 || videos.frameAtSlots[0] != referenceAnchor {
		t.Errorf("FrameAt called for slots %v, want exactly [%d]", videos.frameAtSlots, referenceAnchor)
	}
}

// The cheap gates exist to avoid the expensive ones. A candidate the duration
// or the sequence rejects must never reach a second decode pass.
func TestVerifyVideoUseCase_CheapGatesRunBeforeDecoding(t *testing.T) {
	frames := videoFrames(1, domain.VideoFrameSamples)
	videos := &mockVideoExtractor{}

	uc := usecase.NewVerifyVideoUseCase(
		candidateRepo(storedVideoCert(frames, 4, 60_000)), videos, matchingExtractor())

	if _, err := uc.Execute(context.Background(), usecase.VerifyVideoInput{Content: videoContent()}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(videos.frameAtSlots) != 0 {
		t.Errorf("FrameAt ran %d time(s) for a candidate the duration gate rejected", len(videos.frameAtSlots))
	}
}

// A leaked handle leaks both a spill file and a decode slot, so every exit path
// has to release it exactly once.
func TestVerifyVideoUseCase_ClosesTheHandleExactlyOnce(t *testing.T) {
	frames := videoFrames(1, domain.VideoFrameSamples)

	cases := map[string]struct {
		repo      *mockRepo
		extractor *mockExtractor
	}{
		"on a match":   {repo: candidateRepo(storedVideoCert(frames, 4, 8_000)), extractor: matchingExtractor()},
		"on no match":  {repo: candidateRepo(), extractor: matchingExtractor()},
		"on a failure": {repo: &mockRepo{findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) { return nil, errors.New("db down") }}, extractor: matchingExtractor()},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			handle := &mockVideoHandle{hash: "video-hash", probe: validVideoProbe()}
			videos := &mockVideoExtractor{
				ingestFn: func(_ context.Context, _ io.Reader) (usecase.VideoHandle, error) {
					return handle, nil
				},
			}

			uc := usecase.NewVerifyVideoUseCase(tc.repo, videos, tc.extractor)
			_, _ = uc.Execute(context.Background(), usecase.VerifyVideoInput{Content: videoContent()})

			if handle.closes != 1 {
				t.Errorf("handle closed %d time(s), want exactly 1", handle.closes)
			}
		})
	}
}

// A failing Close is logged, not propagated: the verdict is already decided and
// a temp-file removal problem is not the caller's business.
func TestVerifyVideoUseCase_ToleratesCloseFailure(t *testing.T) {
	handle := &mockVideoHandle{hash: "video-hash", probe: validVideoProbe(), closeErr: errors.New("busy")}
	videos := &mockVideoExtractor{
		ingestFn: func(_ context.Context, _ io.Reader) (usecase.VideoHandle, error) { return handle, nil },
	}

	uc := usecase.NewVerifyVideoUseCase(candidateRepo(), videos, matchingExtractor())
	out, err := uc.Execute(context.Background(), usecase.VerifyVideoInput{Content: videoContent()})
	if err != nil {
		t.Fatalf("a Close failure must not fail the request: %v", err)
	}
	if out.Certified {
		t.Error("expected no match")
	}
}
