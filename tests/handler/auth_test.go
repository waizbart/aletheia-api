package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waizbart/aletheia-api/internal/handler"
	"github.com/waizbart/aletheia-api/internal/observability"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

func TestAdminAuth(t *testing.T) {
	tests := []struct {
		name       string
		token      string
		authHeader string
		wantStatus int
		wantCalled bool
	}{
		{
			name:       "valid bearer token passes through",
			token:      "s3cret",
			authHeader: "Bearer s3cret",
			wantStatus: http.StatusOK,
			wantCalled: true,
		},
		{
			name:       "scheme match is case-insensitive",
			token:      "s3cret",
			authHeader: "bearer s3cret",
			wantStatus: http.StatusOK,
			wantCalled: true,
		},
		{
			name:       "surrounding whitespace is tolerated",
			token:      "s3cret",
			authHeader: "Bearer   s3cret  ",
			wantStatus: http.StatusOK,
			wantCalled: true,
		},
		{
			name:       "wrong token is rejected",
			token:      "s3cret",
			authHeader: "Bearer wrong",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "missing header is rejected",
			token:      "s3cret",
			authHeader: "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "non-bearer scheme is rejected",
			token:      "s3cret",
			authHeader: "Basic s3cret",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "header without a scheme separator is rejected",
			token:      "s3cret",
			authHeader: "s3cret",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "bearer with empty credentials is rejected",
			token:      "s3cret",
			authHeader: "Bearer ",
			wantStatus: http.StatusUnauthorized,
		},
		{
			// The critical default: an operator who forgets to configure the
			// token gets a locked door, never an open one.
			name:       "empty configured token rejects even a matching header",
			token:      "",
			authHeader: "Bearer ",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "empty configured token rejects any token",
			token:      "",
			authHeader: "Bearer anything",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var called bool
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})

			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodDelete, "/certificates/abc", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			handler.AdminAuth(tt.token)(inner).ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if called != tt.wantCalled {
				t.Errorf("inner called = %v, want %v", called, tt.wantCalled)
			}
			if tt.wantStatus == http.StatusUnauthorized {
				if got := rr.Header().Get("WWW-Authenticate"); got == "" {
					t.Error("expected a WWW-Authenticate challenge on 401")
				}
			}
		})
	}
}

// TestRouteGuards pins which routes the admin guard covers. Verification must
// stay public — it is the free half of the product — while deletion must not.
func TestRouteGuards(t *testing.T) {
	const token = "admin-token"

	mux := http.NewServeMux()
	certs := handler.NewCertificateHandler(
		&mockCertifier{},
		&mockVerifier{executeFn: func(_ context.Context, _ usecase.VerifyInput) (*usecase.VerifyOutput, error) {
			return &usecase.VerifyOutput{Certified: false}, nil
		}},
		&mockDeleter{},
		nil,
		true,
	)
	certs.RegisterRoutes(mux, handler.AdminAuth(token), nil)
	handler.RegisterObservabilityRoutes(mux, observability.NewCollector(4), nil, handler.AdminAuth(token))

	tests := []struct {
		name         string
		method       string
		target       string
		guarded      bool
		wantWithAuth int
	}{
		{
			name:         "delete requires admin",
			method:       http.MethodDelete,
			target:       "/certificates/" + strings.Repeat("a", 64),
			guarded:      true,
			wantWithAuth: http.StatusNoContent,
		},
		{
			name:         "dashboard requires admin",
			method:       http.MethodGet,
			target:       "/observability",
			guarded:      true,
			wantWithAuth: http.StatusOK,
		},
		{
			name:         "trace history requires admin",
			method:       http.MethodGet,
			target:       "/observability/traces",
			guarded:      true,
			wantWithAuth: http.StatusOK,
		},
		{
			name:         "verify by hash stays public",
			method:       http.MethodGet,
			target:       "/certificates/verify?hash=" + strings.Repeat("a", 64),
			guarded:      false,
			wantWithAuth: http.StatusNotFound, // not certified, but reachable
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			anon := httptest.NewRecorder()
			mux.ServeHTTP(anon, httptest.NewRequest(tt.method, tt.target, nil))

			if tt.guarded {
				if anon.Code != http.StatusUnauthorized {
					t.Fatalf("anonymous status = %d, want 401", anon.Code)
				}
			} else if anon.Code == http.StatusUnauthorized {
				t.Fatalf("%s must not require admin credentials", tt.target)
			}

			authed := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.target, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			mux.ServeHTTP(authed, req)

			if authed.Code != tt.wantWithAuth {
				t.Errorf("authenticated status = %d, want %d", authed.Code, tt.wantWithAuth)
			}
		})
	}
}
