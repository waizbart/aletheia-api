package repository_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waizbart/aletheia-api/internal/repository"
)

var (
	validAddr1 = "0x1111111111111111111111111111111111111111"
	validAddr2 = "0x2222222222222222222222222222222222222222"
	validHash  = strings.Repeat("ab", 32)
)

// --- Constructor ---

func TestNewEVMBlockchainService_Valid(t *testing.T) {
	svc, err := repository.NewEVMBlockchainService("http://localhost:8545", validAddr1, validAddr2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
}

func TestNewEVMBlockchainService_InvalidFromAddress(t *testing.T) {
	_, err := repository.NewEVMBlockchainService("http://localhost:8545", "bad", validAddr2)
	if err == nil {
		t.Fatal("expected error for invalid from address")
	}
}

func TestNewEVMBlockchainService_InvalidAnchorAddress(t *testing.T) {
	_, err := repository.NewEVMBlockchainService("http://localhost:8545", validAddr1, "bad")
	if err == nil {
		t.Fatal("expected error for invalid anchor address")
	}
}

func TestNewEVMBlockchainService_EmptyRPCURL(t *testing.T) {
	_, err := repository.NewEVMBlockchainService("", validAddr1, validAddr2)
	if err == nil {
		t.Fatal("expected error for empty RPC URL")
	}
}

func TestNewEVMBlockchainService_AddressValidation(t *testing.T) {
	tests := []struct {
		name string
		addr string
		ok   bool
	}{
		{"valid lowercase", "0xabcdef1234567890abcdef1234567890abcdef12", true},
		{"valid uppercase", "0xABCDEF1234567890ABCDEF1234567890ABCDEF12", true},
		{"too short", "0x1234", false},
		{"no 0x prefix", "1111111111111111111111111111111111111111ff", false},
		{"non-hex chars", "0xGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := repository.NewEVMBlockchainService("http://localhost:8545", tt.addr, validAddr2)
			if tt.ok && err != nil {
				t.Fatalf("expected success, got error: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// --- RegisterHash ---

func TestRegisterHash_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0xtxhash123"}`))
	}))
	defer server.Close()

	svc, _ := repository.NewEVMBlockchainService(server.URL, validAddr1, validAddr2)
	tx, block, err := svc.RegisterHash(context.Background(), validHash)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tx != "0xtxhash123" {
		t.Errorf("tx = %q, want 0xtxhash123", tx)
	}
	if block != 0 {
		t.Errorf("block = %d, want 0", block)
	}
}

func TestRegisterHash_InvalidHash(t *testing.T) {
	svc, _ := repository.NewEVMBlockchainService("http://localhost:8545", validAddr1, validAddr2)

	_, _, err := svc.RegisterHash(context.Background(), "tooshort")
	if err == nil {
		t.Fatal("expected error for invalid hash")
	}
}

func TestRegisterHash_InvalidHexInHash(t *testing.T) {
	svc, _ := repository.NewEVMBlockchainService("http://localhost:8545", validAddr1, validAddr2)

	badHash := strings.Repeat("zz", 32)
	_, _, err := svc.RegisterHash(context.Background(), badHash)
	if err == nil {
		t.Fatal("expected error for non-hex hash")
	}
}

func TestRegisterHash_WithOxPrefix(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0xtx"}`))
	}))
	defer server.Close()

	svc, _ := repository.NewEVMBlockchainService(server.URL, validAddr1, validAddr2)
	_, _, err := svc.RegisterHash(context.Background(), "0x"+validHash)
	if err != nil {
		t.Fatalf("unexpected error with 0x prefix: %v", err)
	}
}

func TestRegisterHash_RPCError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"message":"insufficient funds"}}`))
	}))
	defer server.Close()

	svc, _ := repository.NewEVMBlockchainService(server.URL, validAddr1, validAddr2)
	_, _, err := svc.RegisterHash(context.Background(), validHash)
	if err == nil || !strings.Contains(err.Error(), "insufficient funds") {
		t.Fatalf("expected rpc error, got: %v", err)
	}
}

func TestRegisterHash_EmptyResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":""}`))
	}))
	defer server.Close()

	svc, _ := repository.NewEVMBlockchainService(server.URL, validAddr1, validAddr2)
	_, _, err := svc.RegisterHash(context.Background(), validHash)
	if err == nil || !strings.Contains(err.Error(), "empty transaction hash") {
		t.Fatalf("expected empty tx error, got: %v", err)
	}
}

func TestRegisterHash_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer server.Close()

	svc, _ := repository.NewEVMBlockchainService(server.URL, validAddr1, validAddr2)
	_, _, err := svc.RegisterHash(context.Background(), validHash)
	if err == nil || !strings.Contains(err.Error(), "decode rpc response") {
		t.Fatalf("expected decode error, got: %v", err)
	}
}

func TestRegisterHash_HTTPFailure(t *testing.T) {
	svc, _ := repository.NewEVMBlockchainService("http://127.0.0.1:1", validAddr1, validAddr2)
	_, _, err := svc.RegisterHash(context.Background(), validHash)
	if err == nil || !strings.Contains(err.Error(), "send rpc request") {
		t.Fatalf("expected connection error, got: %v", err)
	}
}

// --- IsHashRegistered ---

func TestIsHashRegistered_ReturnsFalse(t *testing.T) {
	svc, _ := repository.NewEVMBlockchainService("http://localhost:8545", validAddr1, validAddr2)
	registered, err := svc.IsHashRegistered(context.Background(), validHash)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if registered {
		t.Error("expected false, got true")
	}
}

// --- Factory ---

func TestNewBlockchainServiceFromEnv_ErrorsWhenEnvMissing(t *testing.T) {
	t.Setenv("RPC_URL", "")
	t.Setenv("FROM_ADDRESS", "")
	t.Setenv("CONTRACT_ADDRESS", "")

	_, err := repository.NewBlockchainServiceFromEnv()
	if err == nil {
		t.Fatal("expected error when env vars are missing")
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

func TestNewBlockchainServiceFromEnv_UsesRPCServiceWhenConfigured(t *testing.T) {
	t.Setenv("RPC_URL", "http://localhost:8545")
	t.Setenv("FROM_ADDRESS", "0x1111111111111111111111111111111111111111")
	t.Setenv("CONTRACT_ADDRESS", "0x2222222222222222222222222222222222222222")

	svc, err := repository.NewBlockchainServiceFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := svc.(*repository.RPCBlockchainService); !ok {
		t.Fatalf("expected RPCBlockchainService, got %T", svc)
	}
}
