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
		name     string
		repo     *mockRepo
		input    usecase.VerifyInput
		wantCert bool
		wantErr  string
	}{
		{
			name: "by hash found",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, hash string) (*domain.Certificate, error) {
					return &domain.Certificate{ContentHash: hash}, nil
				},
			},
			input:    usecase.VerifyInput{Hash: "abc123"},
			wantCert: true,
		},
		{
			name: "by hash not found",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
			},
			input:    usecase.VerifyInput{Hash: "abc123"},
			wantCert: false,
		},
		{
			name: "by content found",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return &domain.Certificate{ContentHash: "computed"}, nil
				},
			},
			input:    usecase.VerifyInput{Content: strings.NewReader("test content")},
			wantCert: true,
		},
		{
			name:    "no hash no content",
			repo:    &mockRepo{},
			input:   usecase.VerifyInput{},
			wantErr: "no content or hash provided",
		},
		{
			name:    "hash error from broken reader",
			repo:    &mockRepo{},
			input:   usecase.VerifyInput{Content: errReader{}},
			wantErr: "hashing content",
		},
		{
			name: "repo error",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, errors.New("db error")
				},
			},
			input:   usecase.VerifyInput{Hash: "abc123"},
			wantErr: "db error",
		},
		{
			name: "by perceptual hash fallback",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
				findByPerceptualHashFn: func(_ context.Context, _ uint64, maxDistance int) (*domain.Certificate, error) {
					if maxDistance != 8 {
						t.Fatalf("maxDistance = %d, want 8", maxDistance)
					}
					return &domain.Certificate{ContentHash: "perceptual"}, nil
				},
			},
			input:    usecase.VerifyInput{Content: bytes.NewReader(sampleJPEG(t))},
			wantCert: true,
		},
		{
			name: "perceptual fallback repo error",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
				findByPerceptualHashFn: func(_ context.Context, _ uint64, _ int) (*domain.Certificate, error) {
					return nil, errors.New("perceptual db error")
				},
			},
			input:   usecase.VerifyInput{Content: bytes.NewReader(sampleJPEG(t))},
			wantErr: "perceptual db error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc := usecase.NewVerifyUseCase(tt.repo)
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
