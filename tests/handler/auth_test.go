package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waizbart/aletheia-api/internal/handler"
)

func TestAPIKeyAuth(t *testing.T) {
	keys := map[string]string{"good-key": "alice"}

	// Handler echoes the authenticated identity so we can assert it was injected.
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(handler.IdentityFromContext(r.Context())))
	})
	h := handler.APIKeyAuth(keys)(next)

	tests := []struct {
		name       string
		path       string
		key        string
		wantStatus int
		wantBody   string
	}{
		{"no key on protected route", "/certificates", "", http.StatusUnauthorized, ""},
		{"invalid key", "/certificates", "wrong", http.StatusUnauthorized, ""},
		{"valid key injects identity", "/certificates", "good-key", http.StatusOK, "alice"},
		{"health is exempt", "/health", "", http.StatusOK, ""},
		{"docs is exempt", "/docs", "", http.StatusOK, ""},
		{"observability is exempt", "/observability/events", "", http.StatusOK, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.key != "" {
				req.Header.Set("X-API-Key", tc.key)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tc.wantStatus)
			}
			if tc.wantStatus == http.StatusOK && rr.Body.String() != tc.wantBody {
				t.Errorf("identity body = %q, want %q", rr.Body.String(), tc.wantBody)
			}
		})
	}
}

func TestIdentityFromContext_Empty(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if id := handler.IdentityFromContext(req.Context()); id != "" {
		t.Errorf("expected empty identity, got %q", id)
	}
}
