package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

func TestDeleteUseCase_Execute(t *testing.T) {
	const hash = "abc123"

	tests := []struct {
		name        string
		input       usecase.DeleteInput
		repoDelErr  error
		wantErr     bool
		wantErrIs   error
		wantRepoDel bool
	}{
		{
			name:    "empty hash is rejected",
			input:   usecase.DeleteInput{Hash: ""},
			wantErr: true,
		},
		{
			name:        "missing certificate returns not found",
			input:       usecase.DeleteInput{Hash: hash},
			repoDelErr:  domain.ErrNotFound,
			wantErr:     true,
			wantErrIs:   domain.ErrNotFound,
			wantRepoDel: true,
		},
		{
			name:        "deletes row",
			input:       usecase.DeleteInput{Hash: hash},
			wantRepoDel: true,
		},
		{
			name:        "row delete failure is propagated",
			input:       usecase.DeleteInput{Hash: hash},
			repoDelErr:  errors.New("delete failed"),
			wantErr:     true,
			wantRepoDel: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rowDeleted bool
			var deletedHash string

			repo := &mockRepo{
				deleteFn: func(_ context.Context, h string) error {
					rowDeleted = true
					deletedHash = h
					return tt.repoDelErr
				},
			}

			uc := usecase.NewDeleteUseCase(repo)
			err := uc.Execute(context.Background(), tt.input)

			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
				t.Fatalf("error = %v, want errors.Is %v", err, tt.wantErrIs)
			}
			if rowDeleted != tt.wantRepoDel {
				t.Errorf("row deleted = %v, want %v", rowDeleted, tt.wantRepoDel)
			}
			if tt.wantRepoDel && rowDeleted && deletedHash != tt.input.Hash {
				t.Errorf("deleted hash = %q, want %q", deletedHash, tt.input.Hash)
			}
		})
	}
}
