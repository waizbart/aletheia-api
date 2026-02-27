package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

func TestCertifyUseCase_Execute(t *testing.T) {
	tests := []struct {
		name    string
		repo    *mockRepo
		chain   *mockBlockchain
		input   usecase.CertifyInput
		wantErr string
	}{
		{
			name: "happy path",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return nil, nil
				},
				saveFn: func(_ context.Context, _ *domain.Certificate) error {
					return nil
				},
			},
			chain: &mockBlockchain{
				registerHashFn: func(_ context.Context, _ string) (string, uint64, error) {
					return "0xabc", 1, nil
				},
			},
			input: usecase.CertifyInput{
				Content:    strings.NewReader("test content"),
				Registrant: "tester",
			},
		},
		{
			name: "already certified",
			repo: &mockRepo{
				findByHashFn: func(_ context.Context, hash string) (*domain.Certificate, error) {
					return &domain.Certificate{ContentHash: hash}, nil
				},
			},
			chain: &mockBlockchain{},
			input: usecase.CertifyInput{
				Content:    strings.NewReader("test content"),
				Registrant: "tester",
			},
			wantErr: "already certified",
		},
		{
			name:  "hash error",
			repo:  &mockRepo{},
			chain: &mockBlockchain{},
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
			chain: &mockBlockchain{},
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
			input: usecase.CertifyInput{
				Content:    strings.NewReader("test content"),
				Registrant: "tester",
			},
			wantErr: "saving certificate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc := usecase.NewCertifyUseCase(tt.repo, tt.chain)
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
		})
	}
}
