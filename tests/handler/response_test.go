package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waizbart/aletheia-api/internal/handler"
)

func TestJSONResponseContentType(t *testing.T) {
	mux := http.NewServeMux()
	handler.RegisterHealthRoutes(mux)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	mux.ServeHTTP(rr, req)

	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestErrorResponseFormat(t *testing.T) {
	mux := http.NewServeMux()
	cert := handler.NewCertificateHandler(&mockCertifier{}, &mockVerifier{})
	cert.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/certificates/verify", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}

	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := body["error"]; !ok {
		t.Error("response missing 'error' key")
	}
}
