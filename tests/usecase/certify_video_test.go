package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

func TestCertifyVideoUseCase_Execute(t *testing.T) {
	reduction := validReduction()

	tests := []struct {
		name    string
		repo    *mockRepo
		videos  *mockVideoExtractor
		input   usecase.CertifyVideoInput
		wantErr string
		check   func(t *testing.T, out *usecase.CertifyOutput)
	}{
		{
			name: "happy path stores the anchor frame in the ordinary feature columns",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
				saveFn: func(_ context.Context, cert *domain.Certificate) error {
					if cert.MediaKind != domain.MediaKindVideo {
						t.Errorf("MediaKind = %q, want %q", cert.MediaKind, domain.MediaKindVideo)
					}
					// The anchor frame's hash and signature go into the same
					// columns an image uses, which is what lets the image
					// matcher run against video unchanged.
					if cert.PHash == nil || *cert.PHash != reduction.AnchorPHash {
						t.Error("certificate does not carry the anchor frame's pHash")
					}
					if cert.Signature != reduction.Anchor {
						t.Error("certificate does not carry the anchor frame's signature")
					}
					if cert.Video == nil || cert.Video.AnchorIndex != reduction.Signature.AnchorIndex {
						t.Errorf("video signature not persisted: %+v", cert.Video)
					}
					if cert.DurationMs != reduction.Signature.DurationMs {
						t.Errorf("DurationMs = %d, want %d", cert.DurationMs, reduction.Signature.DurationMs)
					}

					want := domain.VideoFeatureCommitment(
						&reduction.AnchorPHash, reduction.Anchor, reduction.Signature)
					if cert.FeatureCommitment == nil || *cert.FeatureCommitment != want {
						t.Error("commitment is not the video variant over the stored values")
					}
					// A video commitment must never be the image one, or a
					// video could be passed off as a certified still.
					if imageOnly := domain.FeatureCommitment(&reduction.AnchorPHash, reduction.Anchor); cert.FeatureCommitment != nil && *cert.FeatureCommitment == imageOnly {
						t.Error("commitment collided with the image commitment")
					}
					return nil
				},
			},
			// The mock hands back the very reduction this test holds, so the
			// assertions can compare identity rather than re-derive it.
			videos: &mockVideoExtractor{
				reduceFn: func(_ context.Context, _ usecase.VideoHandle) (*usecase.VideoReduction, error) {
					return reduction, nil
				},
			},
			input: usecase.CertifyVideoInput{
				Handle:     &mockVideoHandle{hash: "abc", probe: validVideoProbe()},
				Registrant: "org-1",
				OrgID:      "org-1",
				DeviceID:   "device-1",
			},
			check: func(t *testing.T, out *usecase.CertifyOutput) {
				if out.Certificate.ContentHash != "abc" {
					t.Errorf("ContentHash = %q, want abc", out.Certificate.ContentHash)
				}
			},
		},
		{
			name:    "handle is required",
			repo:    &mockRepo{},
			videos:  &mockVideoExtractor{},
			input:   usecase.CertifyVideoInput{},
			wantErr: "handle is required",
		},
		{
			name: "already certified",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, hash string) (*domain.Certificate, error) {
					return &domain.Certificate{ContentHash: hash}, nil
				},
			},
			videos:  &mockVideoExtractor{},
			input:   usecase.CertifyVideoInput{Handle: &mockVideoHandle{hash: "dup"}},
			wantErr: "already certified",
		},
		{
			name: "duplicate lookup failure surfaces",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, errors.New("db down")
				},
			},
			videos:  &mockVideoExtractor{},
			input:   usecase.CertifyVideoInput{Handle: &mockVideoHandle{hash: "x"}},
			wantErr: "db down",
		},
		{
			// Mirrors the image path: extraction failure is not fatal, because
			// the certificate still proves an attested device signed these
			// bytes and exact SHA-256 verification still works.
			name: "reduction failure still certifies, with nothing but the hash",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
				saveFn: func(_ context.Context, cert *domain.Certificate) error {
					if cert.PHash != nil || cert.Signature != nil || cert.Video != nil {
						t.Error("a failed reduction must leave the perceptual columns empty")
					}
					// Duration is known but uncommitted, so it is deliberately
					// not stored: a certificate asserting uncommitted facts is
					// the hole the commitment exists to close.
					if cert.DurationMs != 0 {
						t.Errorf("DurationMs = %d, want 0 for a failed reduction", cert.DurationMs)
					}
					if want := domain.VideoFeatureCommitment(nil, nil, nil); *cert.FeatureCommitment != want {
						t.Error("commitment is not the empty video commitment")
					}
					return nil
				},
			},
			videos: &mockVideoExtractor{
				reduceFn: func(_ context.Context, _ usecase.VideoHandle) (*usecase.VideoReduction, error) {
					return nil, errors.New("no frames")
				},
			},
			input: usecase.CertifyVideoInput{
				Handle: &mockVideoHandle{hash: "partial", probe: validVideoProbe()},
			},
		},
		{
			name: "save failure surfaces",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
				saveFn: func(_ context.Context, _ *domain.Certificate) error {
					return errors.New("disk full")
				},
			},
			videos:  &mockVideoExtractor{},
			input:   usecase.CertifyVideoInput{Handle: &mockVideoHandle{hash: "y", probe: validVideoProbe()}},
			wantErr: "disk full",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc := usecase.NewCertifyVideoUseCase(tt.repo, tt.videos)
			out, err := uc.Execute(context.Background(), tt.input)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out == nil || out.Certificate == nil {
				t.Fatal("expected a certificate")
			}
			if tt.check != nil {
				tt.check(t, out)
			}
		})
	}
}

// Certification does not own the handle: the capture use case opened it to
// verify the signature and closes it once certification returns. Closing it
// here would pull the spill file out from under a caller that still needs it.
func TestCertifyVideoUseCase_DoesNotCloseTheHandle(t *testing.T) {
	handle := &mockVideoHandle{hash: "abc", probe: validVideoProbe()}
	repo := &mockRepo{
		findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) { return nil, nil },
		saveFn:       func(_ context.Context, _ *domain.Certificate) error { return nil },
	}

	uc := usecase.NewCertifyVideoUseCase(repo, &mockVideoExtractor{})
	if _, err := uc.Execute(context.Background(), usecase.CertifyVideoInput{Handle: handle}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handle.closes != 0 {
		t.Errorf("handle closed %d time(s); certification does not own it", handle.closes)
	}
}

// A single-frame reduction has no consecutive pair to measure, which the
// entropy report has to handle rather than divide by zero.
func TestCertifyVideoUseCase_SingleFrameReduction(t *testing.T) {
	frames := videoFrames(3, 1)
	repo := &mockRepo{
		findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) { return nil, nil },
		saveFn: func(_ context.Context, cert *domain.Certificate) error {
			if cert.Video == nil || cert.Video.FrameCount() != 1 {
				t.Errorf("frame count = %d, want 1", cert.Video.FrameCount())
			}
			return nil
		},
	}
	videos := &mockVideoExtractor{
		reduceFn: func(_ context.Context, _ usecase.VideoHandle) (*usecase.VideoReduction, error) {
			return &usecase.VideoReduction{
				Signature: &domain.VideoSignature{
					FramePHashes: frames, AnchorIndex: 0, DurationMs: 400,
				},
				Anchor:      signatureWithGrid(),
				AnchorPHash: frames[0],
				AnchorFrame: []byte("frame"),
			}, nil
		},
	}

	uc := usecase.NewCertifyVideoUseCase(repo, videos)
	if _, err := uc.Execute(context.Background(), usecase.CertifyVideoInput{
		Handle: &mockVideoHandle{hash: "single", probe: validVideoProbe()},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
