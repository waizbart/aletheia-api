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

func TestCertifyUseCase_Execute(t *testing.T) {
	tests := []struct {
		name      string
		repo      *mockRepo
		chain     *mockBlockchain
		extractor *mockExtractor
		blobs     *mockBlobStore
		input     usecase.CertifyInput
		wantErr   string
		check     func(t *testing.T, out *usecase.CertifyOutput)
	}{
		{
			name: "happy path non-image",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
				saveFn: func(_ context.Context, cert *domain.Certificate) error {
					if cert.PHash != nil {
						t.Fatal("expected nil pHash for non-image")
					}
					if cert.Signature != nil {
						t.Fatal("expected nil signature for non-image")
					}
					if cert.ImageBlobKey != "" {
						t.Fatal("expected empty blob key for non-image")
					}
					return nil
				},
			},
			chain: &mockBlockchain{
				registerHashFn: func(_ context.Context, _ string) (string, uint64, error) {
					return "0xabc", 1, nil
				},
			},
			extractor: &mockExtractor{},
			blobs:     &mockBlobStore{},
			input: usecase.CertifyInput{
				Content:    strings.NewReader("not an image"),
				Registrant: "tester",
			},
		},
		{
			name: "happy path image with signature and blob",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
				saveFn: func(_ context.Context, cert *domain.Certificate) error {
					if cert.PHash == nil {
						t.Fatal("expected pHash for image content")
					}
					if cert.Signature == nil {
						t.Fatal("expected signature for image content")
					}
					if cert.ImageBlobKey == "" {
						t.Fatal("expected blob key for image content")
					}
					return nil
				},
			},
			chain: &mockBlockchain{
				registerHashFn: func(_ context.Context, _ string) (string, uint64, error) {
					return "0xabc", 1, nil
				},
			},
			extractor: &mockExtractor{
				computeFn: func(_ context.Context, _ []byte) (*domain.FeatureSignature, []byte, error) {
					return &domain.FeatureSignature{Descriptors: []byte{0x01}, Keypoints: []byte{0x02}}, []byte("jpeg"), nil
				},
			},
			blobs: &mockBlobStore{
				putFn: func(_ context.Context, key string, data []byte) error {
					if !strings.HasSuffix(key, ".jpg") {
						t.Fatalf("blob key should end with .jpg, got %q", key)
					}
					if len(data) == 0 {
						t.Fatal("blob data is empty")
					}
					return nil
				},
			},
			input: usecase.CertifyInput{
				Content:    bytes.NewReader(sampleJPEGForCertify(t)),
				Registrant: "tester",
			},
		},
		{
			name: "extractor failure does not block certify",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
				saveFn: func(_ context.Context, cert *domain.Certificate) error {
					if cert.Signature != nil {
						t.Fatal("expected nil signature when extractor fails")
					}
					if cert.PHash == nil {
						t.Fatal("expected pHash even when extractor fails")
					}
					return nil
				},
			},
			chain: &mockBlockchain{
				registerHashFn: func(_ context.Context, _ string) (string, uint64, error) {
					return "0xabc", 1, nil
				},
			},
			extractor: &mockExtractor{
				computeFn: func(_ context.Context, _ []byte) (*domain.FeatureSignature, []byte, error) {
					return nil, nil, errors.New("no features")
				},
			},
			blobs: &mockBlobStore{},
			input: usecase.CertifyInput{
				Content:    bytes.NewReader(sampleJPEGForCertify(t)),
				Registrant: "tester",
			},
		},
		{
			name: "blob store error aborts certify",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
			},
			chain:     &mockBlockchain{},
			extractor: &mockExtractor{},
			blobs: &mockBlobStore{
				putFn: func(_ context.Context, _ string, _ []byte) error {
					return errors.New("s3 down")
				},
			},
			input: usecase.CertifyInput{
				Content:    bytes.NewReader(sampleJPEGForCertify(t)),
				Registrant: "tester",
			},
			wantErr: "storing image blob",
		},
		{
			name: "already certified",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, hash string) (*domain.Certificate, error) {
					return &domain.Certificate{ContentHash: hash}, nil
				},
			},
			chain:     &mockBlockchain{},
			extractor: &mockExtractor{},
			blobs:     &mockBlobStore{},
			input: usecase.CertifyInput{
				Content:    strings.NewReader("test content"),
				Registrant: "tester",
			},
			wantErr: "already certified",
		},
		{
			name:      "hash error",
			repo:      &mockRepo{},
			chain:     &mockBlockchain{},
			extractor: &mockExtractor{},
			blobs:     &mockBlobStore{},
			input: usecase.CertifyInput{
				Content:    errReader{},
				Registrant: "tester",
			},
			wantErr: "hashing content",
		},
		{
			name: "find by hash error",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, errors.New("db error")
				},
			},
			chain:     &mockBlockchain{},
			extractor: &mockExtractor{},
			blobs:     &mockBlobStore{},
			input: usecase.CertifyInput{
				Content:    strings.NewReader("test content"),
				Registrant: "tester",
			},
			wantErr: "checking existing",
		},
		{
			name: "blockchain register error",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
			},
			chain: &mockBlockchain{
				registerHashFn: func(_ context.Context, _ string) (string, uint64, error) {
					return "", 0, errors.New("chain error")
				},
			},
			extractor: &mockExtractor{},
			blobs:     &mockBlobStore{},
			input: usecase.CertifyInput{
				Content:    strings.NewReader("test content"),
				Registrant: "tester",
			},
			wantErr: "registering on chain",
		},
		{
			name: "save error",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
				saveFn: func(_ context.Context, _ *domain.Certificate) error {
					return errors.New("save error")
				},
			},
			chain: &mockBlockchain{
				registerHashFn: func(_ context.Context, _ string) (string, uint64, error) {
					return "0xabc", 1, nil
				},
			},
			extractor: &mockExtractor{},
			blobs:     &mockBlobStore{},
			input: usecase.CertifyInput{
				Content:    strings.NewReader("test content"),
				Registrant: "tester",
			},
			wantErr: "saving certificate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc := usecase.NewCertifyUseCase(tt.repo, tt.chain, tt.extractor, tt.blobs)
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
			if out == nil || out.Certificate == nil {
				t.Fatal("expected non-nil output with certificate")
			}
			if out.Certificate.Registrant != tt.input.Registrant {
				t.Errorf("registrant = %q, want %q", out.Certificate.Registrant, tt.input.Registrant)
			}
			if tt.check != nil {
				tt.check(t, out)
			}
		})
	}
}

func sampleJPEGForCertify(t *testing.T) []byte {
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
