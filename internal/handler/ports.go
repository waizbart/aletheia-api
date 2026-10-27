package handler

import (
	"context"

	"github.com/waizbart/aletheia-api/internal/usecase"
)

type Certifier interface {
	Execute(ctx context.Context, in usecase.CertifyInput) (*usecase.CertifyOutput, error)
}

type Verifier interface {
	Execute(ctx context.Context, in usecase.VerifyInput) (*usecase.VerifyOutput, error)
}

// VideoVerifier is the video half of verification. It is a separate port rather
// than a flag on Verifier because the two take different inputs and neither
// adapter should have to know about the other's media.
type VideoVerifier interface {
	Execute(ctx context.Context, in usecase.VerifyVideoInput) (*usecase.VerifyOutput, error)
}

type Deleter interface {
	Execute(ctx context.Context, in usecase.DeleteInput) error
}
