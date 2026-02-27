package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waizbart/aletheia-api/internal/handler"
)

func TestDocsEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	handler.RegisterDocsRoutes(mux)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}

	if rr.Body.Len() == 0 {
		t.Error("response body is empty")
	}
}

func TestOpenAPISpecEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	handler.RegisterDocsRoutes(mux)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/docs/openapi.yaml", nil)
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/yaml") {
		t.Errorf("Content-Type = %q, want application/yaml", ct)
	}

	if rr.Body.Len() == 0 {
		t.Error("response body is empty")
	}
}
