package usecase_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

func TestThumbnailUseCase_Execute(t *testing.T) {
	const hash = "abc123"

	tests := []struct {
		name      string
		found     *domain.Certificate
		findErr   error
		renderErr error
		wantPNG   []byte
		wantErr   bool
		wantErrIs error
	}{
		{
			name:    "renders grid for certificate with color grid",
			found:   &domain.Certificate{ID: "1", ContentHash: hash, Signature: signatureWithGrid()},
			wantPNG: []byte("png"),
		},
		{
			name:      "missing certificate returns not found",
			found:     nil,
			wantErr:   true,
			wantErrIs: domain.ErrNotFound,
		},
		{
			name:      "certificate without signature returns not found",
			found:     &domain.Certificate{ID: "1", ContentHash: hash},
			wantErr:   true,
			wantErrIs: domain.ErrNotFound,
		},
		{
			name: "certificate with malformed grid returns not found",
			found: &domain.Certificate{ID: "1", ContentHash: hash, Signature: &domain.FeatureSignature{
				Descriptors: []byte{0x01},
				ColorGrid:   []byte{1, 2, 3},
				RefWidth:    10,
				RefHeight:   10,
			}},
			wantErr:   true,
			wantErrIs: domain.ErrNotFound,
		},
		{
			name:    "repo error is propagated",
			findErr: errors.New("db down"),
			wantErr: true,
		},
		{
			name:      "renderer error is propagated",
			found:     &domain.Certificate{ID: "1", ContentHash: hash, Signature: signatureWithGrid()},
			renderErr: errors.New("render boom"),
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &mockRepo{
				findByHashFn: func(_ context.Context, h string) (*domain.Certificate, error) {
					if h != hash {
						t.Errorf("lookup hash = %q, want %q", h, hash)
					}
					return tt.found, tt.findErr
				},
			}
			renderer := &mockRenderer{
				renderFn: func(grid []byte, refW, refH int) ([]byte, error) {
					if tt.renderErr != nil {
						return nil, tt.renderErr
					}
					if len(grid) != domain.ColorGridBytes {
						t.Errorf("renderer received grid of %d bytes, want %d", len(grid), domain.ColorGridBytes)
					}
					if refW <= 0 || refH <= 0 {
						t.Errorf("renderer received invalid dims %dx%d", refW, refH)
					}
					return []byte("png"), nil
				},
			}

			uc := usecase.NewThumbnailUseCase(repo, renderer)
			png, err := uc.Execute(context.Background(), hash)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
					t.Fatalf("error = %v, want errors.Is %v", err, tt.wantErrIs)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Equal(png, tt.wantPNG) {
				t.Errorf("png = %q, want %q", png, tt.wantPNG)
			}
		})
	}
}
