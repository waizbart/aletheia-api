package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/handler"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

var fixedTime = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

func certifyOK(_ context.Context, _ usecase.CertifyInput) (*usecase.CertifyOutput, error) {
	return &usecase.CertifyOutput{
		Certificate: &domain.Certificate{
			ID:          "1",
			ContentHash: "abc123",
			Registrant:  "tester",
			TxHash:      "0xdef",
			BlockNumber: 1,
			CreatedAt:   fixedTime,
		},
	}, nil
}

func verifyFound(_ context.Context, _ usecase.VerifyInput) (*usecase.VerifyOutput, error) {
	return &usecase.VerifyOutput{
		Certified: true,
		Certificate: &domain.Certificate{
			ID:          "1",
			ContentHash: "abc123",
			TxHash:      "0xdef",
			BlockNumber: 1,
			CreatedAt:   fixedTime,
		},
	}, nil
}

func verifyNotFound(_ context.Context, _ usecase.VerifyInput) (*usecase.VerifyOutput, error) {
	return &usecase.VerifyOutput{Certified: false}, nil
}

func verifyErr(_ context.Context, _ usecase.VerifyInput) (*usecase.VerifyOutput, error) {
	return nil, fmt.Errorf("internal failure")
}

func setupMux(cert *mockCertifier, ver *mockVerifier) *http.ServeMux {
	mux := http.NewServeMux()
	h := handler.NewCertificateHandler(cert, ver)
	h.RegisterRoutes(mux)
	return mux
}

func TestHandleCertify_ValidUpload(t *testing.T) {
	mux := setupMux(&mockCertifier{executeFn: certifyOK}, &mockVerifier{})

	req := newUploadRequest(t, http.MethodPost, "/certificates", "image/png", []byte("img"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusCreated)
	}

	var body map[string]any
	json.NewDecoder(rr.Body).Decode(&body)
	if body["content_hash"] != "abc123" {
		t.Errorf("content_hash = %v, want abc123", body["content_hash"])
	}
}

func TestHandleCertify_MissingFile(t *testing.T) {
	mux := setupMux(&mockCertifier{}, &mockVerifier{})

	req := httptest.NewRequest(http.MethodPost, "/certificates", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleCertify_UnsupportedMediaType(t *testing.T) {
	mux := setupMux(&mockCertifier{}, &mockVerifier{})

	req := newUploadRequest(t, http.MethodPost, "/certificates", "text/plain", []byte("data"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnsupportedMediaType)
	}
}

func TestHandleCertify_AlreadyCertified(t *testing.T) {
	cert := &mockCertifier{
		executeFn: func(_ context.Context, _ usecase.CertifyInput) (*usecase.CertifyOutput, error) {
			return nil, fmt.Errorf("certify: %w", domain.ErrAlreadyCertified)
		},
	}
	mux := setupMux(cert, &mockVerifier{})

	req := newUploadRequest(t, http.MethodPost, "/certificates", "image/jpeg", []byte("img"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusConflict)
	}
}

func TestHandleCertify_UseCaseError(t *testing.T) {
	cert := &mockCertifier{
		executeFn: func(_ context.Context, _ usecase.CertifyInput) (*usecase.CertifyOutput, error) {
			return nil, fmt.Errorf("something broke")
		},
	}
	mux := setupMux(cert, &mockVerifier{})

	req := newUploadRequest(t, http.MethodPost, "/certificates", "video/mp4", []byte("vid"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnprocessableEntity)
	}
}

func TestHandleVerifyByHash_Found(t *testing.T) {
	mux := setupMux(&mockCertifier{}, &mockVerifier{executeFn: verifyFound})

	req := httptest.NewRequest(http.MethodGet, "/certificates/verify?hash=abc123", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var body map[string]any
	json.NewDecoder(rr.Body).Decode(&body)
	if body["certified"] != true {
		t.Errorf("certified = %v, want true", body["certified"])
	}
}

func TestHandleVerifyByHash_NotFound(t *testing.T) {
	mux := setupMux(&mockCertifier{}, &mockVerifier{executeFn: verifyNotFound})

	req := httptest.NewRequest(http.MethodGet, "/certificates/verify?hash=unknown", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestHandleVerifyByHash_MissingHash(t *testing.T) {
	mux := setupMux(&mockCertifier{}, &mockVerifier{})

	req := httptest.NewRequest(http.MethodGet, "/certificates/verify", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleVerifyByHash_UseCaseError(t *testing.T) {
	mux := setupMux(&mockCertifier{}, &mockVerifier{executeFn: verifyErr})

	req := httptest.NewRequest(http.MethodGet, "/certificates/verify?hash=abc", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}

func TestHandleVerifyByFile_Found(t *testing.T) {
	mux := setupMux(&mockCertifier{}, &mockVerifier{executeFn: verifyFound})

	req := newUploadRequest(t, http.MethodPost, "/certificates/verify", "image/png", []byte("img"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var body map[string]any
	json.NewDecoder(rr.Body).Decode(&body)
	if body["certified"] != true {
		t.Errorf("certified = %v, want true", body["certified"])
	}
}

func TestHandleVerifyByFile_NotFound(t *testing.T) {
	mux := setupMux(&mockCertifier{}, &mockVerifier{executeFn: verifyNotFound})

	req := newUploadRequest(t, http.MethodPost, "/certificates/verify", "image/jpeg", []byte("img"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestHandleVerifyByFile_MissingFile(t *testing.T) {
	mux := setupMux(&mockCertifier{}, &mockVerifier{})

	req := httptest.NewRequest(http.MethodPost, "/certificates/verify", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleVerifyByFile_UnsupportedType(t *testing.T) {
	mux := setupMux(&mockCertifier{}, &mockVerifier{})

	req := newUploadRequest(t, http.MethodPost, "/certificates/verify", "application/pdf", []byte("pdf"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnsupportedMediaType)
	}
}

func TestHandleVerifyByFile_UseCaseError(t *testing.T) {
	mux := setupMux(&mockCertifier{}, &mockVerifier{executeFn: verifyErr})

	req := newUploadRequest(t, http.MethodPost, "/certificates/verify", "video/webm", []byte("vid"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}

// --- Response format assertions ---

func TestCertifyResponse_ContainsAllFields(t *testing.T) {
	mux := setupMux(&mockCertifier{executeFn: certifyOK}, &mockVerifier{})

	req := newUploadRequest(t, http.MethodPost, "/certificates", "image/png", []byte("img"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	for _, key := range []string{"id", "content_hash", "registrant", "tx_hash", "block_number", "created_at"} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing key %q in response", key)
		}
	}

	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}
