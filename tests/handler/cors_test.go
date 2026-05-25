package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waizbart/aletheia-api/internal/handler"
)

func TestCORS_PreflightShortCircuits(t *testing.T) {
	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	wrapped := handler.CORS([]string{"*"})(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/certificates", nil)
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "x-registrant,content-type")
	wrapped.ServeHTTP(rr, req)

	if called {
		t.Error("preflight reached the inner handler; it should be answered by the middleware")
	}
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "*")
	}
	// Requested headers must be echoed back so the browser permits X-Registrant.
	if got := rr.Header().Get("Access-Control-Allow-Headers"); got != "x-registrant,content-type" {
		t.Errorf("Access-Control-Allow-Headers = %q, want echo of request", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("Access-Control-Allow-Methods is empty")
	}
}

func TestCORS_WildcardOnActualRequest(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	wrapped := handler.CORS([]string{"*"})(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certificates", nil)
	req.Header.Set("Origin", "http://example.com")
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusCreated)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "*")
	}
	// With no preflight echo, the static fallback list must cover X-Registrant.
	if got := rr.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type, Accept, X-Registrant" {
		t.Errorf("Access-Control-Allow-Headers = %q, want fallback list", got)
	}
}

func TestCORS_AllowlistReflectsKnownOrigin(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	wrapped := handler.CORS([]string{"https://app.aletheia.dev", " https://docs.aletheia.dev "})(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/certificates/verify?hash=x", nil)
	req.Header.Set("Origin", "https://docs.aletheia.dev")
	wrapped.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://docs.aletheia.dev" {
		t.Errorf("Access-Control-Allow-Origin = %q, want reflected origin", got)
	}
	if got := rr.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want %q", got, "Origin")
	}
}

func TestCORS_AllowlistRejectsUnknownOrigin(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	wrapped := handler.CORS([]string{"https://app.aletheia.dev"})(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/certificates/verify?hash=x", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	wrapped.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty for disallowed origin", got)
	}
}

func TestCORS_NoOriginHeaderIsUntouched(t *testing.T) {
	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	wrapped := handler.CORS([]string{"*"})(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil) // same-origin / curl: no Origin
	wrapped.ServeHTTP(rr, req)

	if !called {
		t.Fatal("inner handler was not called for a non-CORS request")
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty when no Origin header", got)
	}
}
