package repository_test

import (
	"context"
	"strings"
	"testing"

	"github.com/waizbart/aletheia-api/internal/repository"
)

func TestStubBlockchainService_RegisterHash(t *testing.T) {
	svc := repository.NewStubBlockchainService()
	hash := "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2"

	tx, block, err := svc.RegisterHash(context.Background(), hash)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(tx, "0x") {
		t.Errorf("tx hash %q does not start with 0x", tx)
	}
	if block != 0 {
		t.Errorf("block = %d, want 0", block)
	}
}

func TestStubBlockchainService_IsHashRegistered(t *testing.T) {
	svc := repository.NewStubBlockchainService()

	registered, err := svc.IsHashRegistered(context.Background(), "anything")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if registered {
		t.Error("expected false, got true")
	}
}
