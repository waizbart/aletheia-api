package repository_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waizbart/aletheia-api/internal/repository"
)

func TestNewEVMBlockchainService_Validation(t *testing.T) {
	_, err := repository.NewEVMBlockchainService("", "0x1111111111111111111111111111111111111111", "0x2222222222222222222222222222222222222222")
	if err == nil || !strings.Contains(err.Error(), "rpc url is required") {
		t.Fatalf("expected rpc url error, got %v", err)
	}

	_, err = repository.NewEVMBlockchainService("http://localhost", "bad", "0x2222222222222222222222222222222222222222")
	if err == nil || !strings.Contains(err.Error(), "invalid from address") {
		t.Fatalf("expected invalid from address error, got %v", err)
	}

	_, err = repository.NewEVMBlockchainService(
		"http://localhost",
		"0x111111111111111111111111111111111111111g",
		"0x2222222222222222222222222222222222222222",
	)
	if err == nil || !strings.Contains(err.Error(), "invalid from address") {
		t.Fatalf("expected invalid from address char error, got %v", err)
	}

	_, err = repository.NewEVMBlockchainService("http://localhost", "0x1111111111111111111111111111111111111111", "bad")
	if err == nil || !strings.Contains(err.Error(), "invalid anchor address") {
		t.Fatalf("expected invalid anchor address error, got %v", err)
	}
}

func TestRPCBlockchainService_RegisterHash_ErrorPaths(t *testing.T) {
	svc, err := repository.NewEVMBlockchainService(
		"http://127.0.0.1:1",
		"0x1111111111111111111111111111111111111111",
		"0x2222222222222222222222222222222222222222",
	)
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	validHash := strings.Repeat("a", 64)

	_, _, err = svc.RegisterHash(context.Background(), "short", validHash)
	if err == nil || !strings.Contains(err.Error(), "hash must be 32-byte hex") {
		t.Fatalf("expected content hash length error, got %v", err)
	}

	_, _, err = svc.RegisterHash(context.Background(), validHash, "short")
	if err == nil || !strings.Contains(err.Error(), "feature commitment") {
		t.Fatalf("expected feature commitment error, got %v", err)
	}

	_, _, err = svc.RegisterHash(context.Background(), strings.Repeat("z", 64), validHash)
	if err == nil || !strings.Contains(err.Error(), "decode hash") {
		t.Fatalf("expected decode hash error, got %v", err)
	}

	_, _, err = svc.RegisterHash(context.Background(), validHash, validHash)
	if err == nil || !strings.Contains(err.Error(), "send rpc request") {
		t.Fatalf("expected send rpc request error, got %v", err)
	}
}

func TestRPCBlockchainService_RegisterHash_RPCResponseBranches(t *testing.T) {
	tests := []struct {
		name        string
		response    string
		wantErrPart string
	}{
		{name: "invalid json", response: "not-json", wantErrPart: "decode rpc response"},
		{name: "rpc error", response: `{"error":{"message":"boom"}}`, wantErrPart: "rpc error: boom"},
		{name: "empty result", response: `{"result":""}`, wantErrPart: "empty transaction hash"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.response))
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

			validHash := strings.Repeat("a", 64)
			_, _, err = svc.RegisterHash(context.Background(), validHash, validHash)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrPart) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrPart, err)
			}
		})
	}
}

func TestRPCBlockchainService_RegisterHash_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("content-type = %q, want application/json", got)
		}
		_, _ = w.Write([]byte(`{"result":"0xtxhash"}`))
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

	validHash := strings.Repeat("a", 64)
	txHash, block, err := svc.RegisterHash(context.Background(), validHash, validHash)
	if err != nil {
		t.Fatalf("RegisterHash: %v", err)
	}
	if txHash != "0xtxhash" {
		t.Fatalf("txHash = %q, want 0xtxhash", txHash)
	}
	if block != 0 {
		t.Fatalf("block = %d, want 0", block)
	}
}

func TestRPCBlockchainService_IsHashRegistered(t *testing.T) {
	svc, err := repository.NewEVMBlockchainService(
		"http://localhost",
		"0x1111111111111111111111111111111111111111",
		"0x2222222222222222222222222222222222222222",
	)
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	ok, err := svc.IsHashRegistered(context.Background(), "abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected false")
	}
}
