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
