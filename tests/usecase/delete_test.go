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
	certWithBlob := &domain.Certificate{ID: "1", ContentHash: hash, ImageBlobKey: hash + ".jpg"}
	certNoBlob := &domain.Certificate{ID: "2", ContentHash: hash}

	tests := []struct {
		name        string
		input       usecase.DeleteInput
		found       *domain.Certificate
		findErr     error
		blobErr     error
		repoDelErr  error
		wantErr     bool
		wantErrIs   error
		wantBlobDel bool
		wantRepoDel bool
	}{
		{
			name:    "empty hash is rejected",
			input:   usecase.DeleteInput{Hash: ""},
			wantErr: true,
		},
		{
			name:      "lookup failure is propagated",
			input:     usecase.DeleteInput{Hash: hash},
			findErr:   errors.New("db down"),
			wantErr:   true,
		},
		{
			name:      "missing certificate returns not found",
			input:     usecase.DeleteInput{Hash: hash},
			found:     nil,
			wantErr:   true,
			wantErrIs: domain.ErrNotFound,
		},
		{
			name:        "deletes blob then row when blob key present",
			input:       usecase.DeleteInput{Hash: hash},
			found:       certWithBlob,
			wantBlobDel: true,
			wantRepoDel: true,
		},
		{
			name:        "skips blob delete when no blob key",
			input:       usecase.DeleteInput{Hash: hash},
			found:       certNoBlob,
			wantBlobDel: false,
			wantRepoDel: true,
		},
		{
			name:        "blob delete failure aborts before row delete",
			input:       usecase.DeleteInput{Hash: hash},
			found:       certWithBlob,
			blobErr:     errors.New("s3 down"),
			wantErr:     true,
			wantBlobDel: true,
			wantRepoDel: false,
		},
		{
			name:        "row delete failure is propagated",
			input:       usecase.DeleteInput{Hash: hash},
			found:       certWithBlob,
			repoDelErr:  errors.New("delete failed"),
			wantErr:     true,
			wantBlobDel: true,
			wantRepoDel: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var blobDeleted, rowDeleted bool

			repo := &mockRepo{
				findByHashFn: func(_ context.Context, _ string) (*domain.Certificate, error) {
					return tt.found, tt.findErr
				},
				deleteFn: func(_ context.Context, _ string) error {
					rowDeleted = true
					return tt.repoDelErr
				},
			}
			blobs := &mockBlobStore{
				deleteFn: func(_ context.Context, _ string) error {
					blobDeleted = true
					return tt.blobErr
				},
			}

			uc := usecase.NewDeleteUseCase(repo, blobs)
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
			if blobDeleted != tt.wantBlobDel {
				t.Errorf("blob deleted = %v, want %v", blobDeleted, tt.wantBlobDel)
			}
			if rowDeleted != tt.wantRepoDel {
				t.Errorf("row deleted = %v, want %v", rowDeleted, tt.wantRepoDel)
			}
		})
	}
}
