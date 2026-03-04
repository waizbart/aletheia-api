package repository_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestNewBlockchainServiceFromEnv_UsesStubWhenEnvMissing(t *testing.T) {
	t.Setenv("RPC_URL", "")
	t.Setenv("PRIVATE_KEY", "")
	t.Setenv("CONTRACT_ADDRESS", "")

	svc, err := repository.NewBlockchainServiceFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := svc.(*repository.StubBlockchainService); !ok {
		t.Fatalf("expected stub service, got %T", svc)
	}
}

func TestNewBlockchainServiceFromEnv_InvalidConfig(t *testing.T) {
	t.Setenv("RPC_URL", "http://127.0.0.1:1")
	t.Setenv("FROM_ADDRESS", "0x1111111111111111111111111111111111111111")
	t.Setenv("CONTRACT_ADDRESS", "invalid")

	_, err := repository.NewBlockchainServiceFromEnv()
	if err == nil {
		t.Fatal("expected error for invalid configuration")
	}
}

func TestRPCBlockchainService_RegisterHash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0xtxhash"}`))
	}))
	defer server.Close()

	svc, err := repository.NewEVMBlockchainService(
		server.URL,
		"0x1111111111111111111111111111111111111111",
		"0x2222222222222222222222222222222222222222",
	)
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	tx, block, err := svc.RegisterHash(context.Background(), strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("unexpected register error: %v", err)
	}
	if tx != "0xtxhash" {
		t.Fatalf("tx = %q, want 0xtxhash", tx)
	}
	if block != 0 {
		t.Fatalf("block = %d, want 0", block)
	}
}
