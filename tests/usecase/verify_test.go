package usecase_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

func TestVerifyUseCase_Execute(t *testing.T) {
	tests := []struct {
		name      string
		repo      *mockRepo
		extractor *mockExtractor
		blobs     *mockBlobStore
		input     usecase.VerifyInput
		wantCert  bool
		wantErr   string
	}{
		{
			name: "by hash found",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, hash string) (*domain.Certificate, error) {
					return &domain.Certificate{ContentHash: hash}, nil
				},
			},
			extractor: &mockExtractor{},
			blobs:     &mockBlobStore{},
			input:     usecase.VerifyInput{Hash: "abc123"},
			wantCert:  true,
		},
		{
			name: "by hash not found",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
			},
			extractor: &mockExtractor{},
			blobs:     &mockBlobStore{},
			input:     usecase.VerifyInput{Hash: "abc123"},
			wantCert:  false,
		},
		{
			name: "by content sha256 hit",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return &domain.Certificate{ContentHash: "computed"}, nil
				},
			},
			extractor: &mockExtractor{},
			blobs:     &mockBlobStore{},
			input:     usecase.VerifyInput{Content: strings.NewReader("test content")},
			wantCert:  true,
		},
		{
			name:      "no hash no content",
			repo:      &mockRepo{},
			extractor: &mockExtractor{},
			blobs:     &mockBlobStore{},
			input:     usecase.VerifyInput{},
			wantErr:   "no content or hash provided",
		},
		{
			name:      "hash error from broken reader",
			repo:      &mockRepo{},
			extractor: &mockExtractor{},
			blobs:     &mockBlobStore{},
			input:     usecase.VerifyInput{Content: errReader{}},
			wantErr:   "hashing content",
		},
		{
			name: "repo error",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, errors.New("db error")
				},
			},
			extractor: &mockExtractor{},
			blobs:     &mockBlobStore{},
			input:     usecase.VerifyInput{Hash: "abc123"},
			wantErr:   "db error",
		},
		{
			name: "by content non-image returns not certified",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
			},
			extractor: &mockExtractor{},
			blobs:     &mockBlobStore{},
			input:     usecase.VerifyInput{Content: strings.NewReader("not an image")},
			wantCert:  false,
		},
		{
			name: "phash candidate matches via ORB",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
				findCandidatesByPHashesFn: func(_ context.Context, phashes [][32]byte, maxDist, topK int) ([]*domain.Certificate, error) {
					if len(phashes) != 4 {
						t.Fatalf("expected 4 phash variants, got %d", len(phashes))
					}
					if maxDist != domain.MaxPHashDistance {
						t.Fatalf("maxDist = %d, want %d", maxDist, domain.MaxPHashDistance)
					}
					if topK != 64 {
						t.Fatalf("topK = %d, want 64", topK)
					}
					storedPHash := phashes[0]
					return []*domain.Certificate{{
						ID:           "cert-1",
						ContentHash:  "stored",
						Signature:    &domain.FeatureSignature{Descriptors: []byte{0x01}, Keypoints: []byte{0x02}},
						PHash:        &storedPHash,
						ImageBlobKey: "stored.jpg",
					}}, nil
				},
			},
			extractor: &mockExtractor{
				matchFn: func(_ context.Context, _, _ *domain.FeatureSignature, _, _ []byte) (domain.MatchDecision, error) {
					return domain.MatchDecision{Matched: true, Inliers: 100, ColorMean: 1.0, ColorMax: 5.0, Coverage: 1.0}, nil
				},
			},
			blobs:    &mockBlobStore{},
			input:    usecase.VerifyInput{Content: bytes.NewReader(sampleJPEG(t))},
			wantCert: true,
		},
		{
			name: "phash candidates exist but ORB rejects all",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
				findCandidatesByPHashesFn: func(_ context.Context, _ [][32]byte, _, _ int) ([]*domain.Certificate, error) {
					return []*domain.Certificate{{
						ID:           "cert-1",
						Signature:    &domain.FeatureSignature{Descriptors: []byte{0x01}, Keypoints: []byte{0x02}},
						ImageBlobKey: "stored.jpg",
					}}, nil
				},
			},
			extractor: &mockExtractor{
				matchFn: func(_ context.Context, _, _ *domain.FeatureSignature, _, _ []byte) (domain.MatchDecision, error) {
					return domain.MatchDecision{Matched: false, Inliers: 5}, nil
				},
			},
			blobs:    &mockBlobStore{},
			input:    usecase.VerifyInput{Content: bytes.NewReader(sampleJPEG(t))},
			wantCert: false,
		},
		{
			name: "candidate match reasons cover color and coverage failures",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
				findCandidatesByPHashesFn: func(_ context.Context, _ [][32]byte, _, _ int) ([]*domain.Certificate, error) {
					return []*domain.Certificate{
						{ID: "color-mean", Signature: &domain.FeatureSignature{Descriptors: []byte{0x01}}, ImageBlobKey: "mean.jpg"},
						{ID: "color-max", Signature: &domain.FeatureSignature{Descriptors: []byte{0x01}}, ImageBlobKey: "max.jpg"},
						{ID: "coverage", Signature: &domain.FeatureSignature{Descriptors: []byte{0x01}}, ImageBlobKey: "coverage.jpg"},
					}, nil
				},
			},
			extractor: &mockExtractor{
				matchFn: func() func(context.Context, *domain.FeatureSignature, *domain.FeatureSignature, []byte, []byte) (domain.MatchDecision, error) {
					call := 0
					return func(_ context.Context, _, _ *domain.FeatureSignature, _, _ []byte) (domain.MatchDecision, error) {
						call++
						switch call {
						case 1:
							return domain.MatchDecision{
								Inliers:   domain.MinInliers,
								ColorMean: domain.MaxColorMean + 0.1,
								ColorMax:  domain.MaxCellDist,
								Coverage:  1.0,
							}, nil
						case 2:
							return domain.MatchDecision{
								Inliers:   domain.MinInliers,
								ColorMean: domain.MaxColorMean,
								ColorMax:  domain.MaxCellDist + 0.1,
								Coverage:  1.0,
							}, nil
						default:
							return domain.MatchDecision{
								Inliers:   domain.MinInliers,
								ColorMean: domain.MaxColorMean,
								ColorMax:  domain.MaxCellDist,
								Coverage:  domain.MinAreaCoverage - 0.01,
							}, nil
						}
					}
				}(),
			},
			blobs:    &mockBlobStore{},
			input:    usecase.VerifyInput{Content: bytes.NewReader(sampleJPEG(t))},
			wantCert: false,
		},
		{
			name: "phash returns no candidates",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
				findCandidatesByPHashesFn: func(_ context.Context, _ [][32]byte, _, _ int) ([]*domain.Certificate, error) {
					return nil, nil
				},
			},
			extractor: &mockExtractor{},
			blobs:     &mockBlobStore{},
			input:     usecase.VerifyInput{Content: bytes.NewReader(sampleJPEG(t))},
			wantCert:  false,
		},
		{
			name: "phash query error",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
				findCandidatesByPHashesFn: func(_ context.Context, _ [][32]byte, _, _ int) ([]*domain.Certificate, error) {
					return nil, errors.New("phash db error")
				},
			},
			extractor: &mockExtractor{},
			blobs:     &mockBlobStore{},
			input:     usecase.VerifyInput{Content: bytes.NewReader(sampleJPEG(t))},
			wantErr:   "phash db error",
		},
		{
			name: "extractor compute fails returns not certified",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
			},
			extractor: &mockExtractor{
				computeFn: func(_ context.Context, _ []byte) (*domain.FeatureSignature, []byte, error) {
					return nil, nil, errors.New("no features")
				},
			},
			blobs:    &mockBlobStore{},
			input:    usecase.VerifyInput{Content: bytes.NewReader(sampleJPEG(t))},
			wantCert: false,
		},
		{
			name: "candidates with nil signature or empty blob key are skipped",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
				findCandidatesByPHashesFn: func(_ context.Context, _ [][32]byte, _, _ int) ([]*domain.Certificate, error) {
					return []*domain.Certificate{
						{ID: "no-sig", ImageBlobKey: "x.jpg"},
						{ID: "no-blob", Signature: &domain.FeatureSignature{Descriptors: []byte{0x01}}},
					}, nil
				},
			},
			extractor: &mockExtractor{},
			blobs:     &mockBlobStore{},
			input:     usecase.VerifyInput{Content: bytes.NewReader(sampleJPEG(t))},
			wantCert:  false,
		},
		{
			name: "extractor match error skips candidate",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
				findCandidatesByPHashesFn: func(_ context.Context, _ [][32]byte, _, _ int) ([]*domain.Certificate, error) {
					return []*domain.Certificate{{
						ID:           "cert-1",
						Signature:    &domain.FeatureSignature{Descriptors: []byte{0x01}},
						ImageBlobKey: "stored.jpg",
					}}, nil
				},
			},
			extractor: &mockExtractor{
				matchFn: func(_ context.Context, _, _ *domain.FeatureSignature, _, _ []byte) (domain.MatchDecision, error) {
					return domain.MatchDecision{}, errors.New("match boom")
				},
			},
			blobs:    &mockBlobStore{},
			input:    usecase.VerifyInput{Content: bytes.NewReader(sampleJPEG(t))},
			wantCert: false,
		},
		{
			name: "verify by hash returns repo error wrapped",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, errors.New("hash db down")
				},
			},
			extractor: &mockExtractor{},
			blobs:     &mockBlobStore{},
			input:     usecase.VerifyInput{Content: bytes.NewReader(sampleJPEG(t))},
			wantErr:   "hash db down",
		},
		{
			name: "blob fetch failure skips candidate but continues",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
				findCandidatesByPHashesFn: func(_ context.Context, _ [][32]byte, _, _ int) ([]*domain.Certificate, error) {
					return []*domain.Certificate{
						{ID: "broken", Signature: &domain.FeatureSignature{Descriptors: []byte{0x01}}, ImageBlobKey: "missing.jpg"},
						{ID: "ok", Signature: &domain.FeatureSignature{Descriptors: []byte{0x01}}, ImageBlobKey: "ok.jpg"},
					}, nil
				},
			},
			extractor: &mockExtractor{
				matchFn: func(_ context.Context, _, _ *domain.FeatureSignature, _, _ []byte) (domain.MatchDecision, error) {
					return domain.MatchDecision{Matched: true}, nil
				},
			},
			blobs: &mockBlobStore{
				getFn: func(_ context.Context, key string) ([]byte, error) {
					if key == "missing.jpg" {
						return nil, errors.New("not found")
					}
					return []byte("ref"), nil
				},
			},
			input:    usecase.VerifyInput{Content: bytes.NewReader(sampleJPEG(t))},
			wantCert: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc := usecase.NewVerifyUseCase(tt.repo, tt.extractor, tt.blobs)
			out, err := uc.Execute(context.Background(), tt.input)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Certified != tt.wantCert {
				t.Errorf("certified = %v, want %v", out.Certified, tt.wantCert)
			}
		})
	}
}

func sampleJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 10), G: uint8(y * 10), B: 120, A: 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("encode sample jpeg: %v", err)
	}
	return b.Bytes()
}
