package handler_test

import (
	"context"
	"encoding/json"
	"errors"
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

type mockOrgCreator struct {
	fn func(ctx context.Context, in usecase.CreateOrgInput) (*domain.Org, error)
}

func (m *mockOrgCreator) Execute(ctx context.Context, in usecase.CreateOrgInput) (*domain.Org, error) {
	if m.fn == nil {
		return &domain.Org{
			ID: "org-1", Name: in.Name, Plan: domain.PlanDeveloper,
			Status: domain.OrgActive, CreatedAt: time.Now(),
		}, nil
	}
	return m.fn(ctx, in)
}

type mockKeyIssuer struct {
	fn func(ctx context.Context, orgID, name string) (*usecase.IssueAPIKeyOutput, error)
}

func (m *mockKeyIssuer) Execute(ctx context.Context, orgID, name string) (*usecase.IssueAPIKeyOutput, error) {
	if m.fn == nil {
		return &usecase.IssueAPIKeyOutput{
			Key: &domain.APIKey{
				ID: "key-1", OrgID: orgID, Name: name,
				Prefix: "alk_abc", Hash: "stored-hash", CreatedAt: time.Now(),
			},
			Plaintext: testAPIKey,
		}, nil
	}
	return m.fn(ctx, orgID, name)
}

type mockKeyRevoker struct {
	err error
}

func (m *mockKeyRevoker) Execute(context.Context, string) error { return m.err }

const adminToken = "admin-secret"

func newAdminMux(creator *mockOrgCreator, issuer *mockKeyIssuer, revoker *mockKeyRevoker) *http.ServeMux {
	if creator == nil {
		creator = &mockOrgCreator{}
	}
	if issuer == nil {
		issuer = &mockKeyIssuer{}
	}
	if revoker == nil {
		revoker = &mockKeyRevoker{}
	}

	mux := http.NewServeMux()
	handler.NewAdminHandler(creator, issuer, revoker).RegisterRoutes(mux, handler.AdminAuth(adminToken))
	return mux
}

func adminRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	return req
}

// TestAdminRoutes_RequireAdminToken pins that tenant onboarding is not
// self-serve: a registrant is only worth something if an operator vetted them.
func TestAdminRoutes_RequireAdminToken(t *testing.T) {
	mux := newAdminMux(nil, nil, nil)

	for _, route := range []struct{ method, target string }{
		{http.MethodPost, "/admin/orgs"},
		{http.MethodPost, "/admin/orgs/org-1/keys"},
		{http.MethodDelete, "/admin/keys/key-1"},
	} {
		t.Run(route.method+" "+route.target, func(t *testing.T) {
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, httptest.NewRequest(route.method, route.target, strings.NewReader("{}")))
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rr.Code)
			}
		})
	}

	t.Run("a tenant API key does not unlock admin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/orgs", strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rr.Code)
		}
	})
}

func TestHandleCreateOrg(t *testing.T) {
	t.Run("creates", func(t *testing.T) {
		mux := newAdminMux(nil, nil, nil)

		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, adminRequest(http.MethodPost, "/admin/orgs", `{"name":"Acme","plan":"developer"}`))

		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", rr.Code, rr.Body)
		}
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["id"] != "org-1" || body["name"] != "Acme" {
			t.Errorf("unexpected body %v", body)
		}
	})

	t.Run("rejects a non-JSON body", func(t *testing.T) {
		mux := newAdminMux(nil, nil, nil)

		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, adminRequest(http.MethodPost, "/admin/orgs", "not json"))

		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rr.Code)
		}
	})

	t.Run("surfaces a validation failure as 400", func(t *testing.T) {
		creator := &mockOrgCreator{fn: func(context.Context, usecase.CreateOrgInput) (*domain.Org, error) {
			return nil, fmt.Errorf("create org: %w: unknown plan %q", domain.ErrInvalidInput, "platinum")
		}}
		mux := newAdminMux(creator, nil, nil)

		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, adminRequest(http.MethodPost, "/admin/orgs", `{"name":"Acme","plan":"platinum"}`))

		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "platinum") {
			t.Errorf("a caller-fixable error should say what to fix, got %s", rr.Body)
		}
	})

	// A failing database is not a bad request. Reporting it as one misleads the
	// operator, and echoing the driver's message leaks schema detail.
	t.Run("reports a storage failure as 500 without leaking it", func(t *testing.T) {
		creator := &mockOrgCreator{fn: func(context.Context, usecase.CreateOrgInput) (*domain.Org, error) {
			return nil, errors.New(`pq: relation "orgs" does not exist`)
		}}
		mux := newAdminMux(creator, nil, nil)

		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, adminRequest(http.MethodPost, "/admin/orgs", `{"name":"Acme","plan":"developer"}`))

		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rr.Code)
		}
		if strings.Contains(rr.Body.String(), "pq:") {
			t.Errorf("the driver message must not reach the client, got %s", rr.Body)
		}
	})
}

func TestHandleIssueKey(t *testing.T) {
	t.Run("returns the credential exactly once", func(t *testing.T) {
		mux := newAdminMux(nil, nil, nil)

		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, adminRequest(http.MethodPost, "/admin/orgs/org-1/keys", `{"name":"ci"}`))

		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", rr.Code, rr.Body)
		}

		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["key"] != testAPIKey {
			t.Errorf("key = %v, want the plaintext credential", body["key"])
		}
		// The stored hash must never travel to the client.
		if strings.Contains(rr.Body.String(), "stored-hash") {
			t.Error("the response must not leak the stored hash")
		}
	})

	t.Run("reports an unknown org", func(t *testing.T) {
		issuer := &mockKeyIssuer{fn: func(context.Context, string, string) (*usecase.IssueAPIKeyOutput, error) {
			return nil, domain.ErrNotFound
		}}
		mux := newAdminMux(nil, issuer, nil)

		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, adminRequest(http.MethodPost, "/admin/orgs/nope/keys", `{}`))

		if rr.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rr.Code)
		}
	})

	t.Run("reports a storage failure", func(t *testing.T) {
		issuer := &mockKeyIssuer{fn: func(context.Context, string, string) (*usecase.IssueAPIKeyOutput, error) {
			return nil, errors.New("db down")
		}}
		mux := newAdminMux(nil, issuer, nil)

		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, adminRequest(http.MethodPost, "/admin/orgs/org-1/keys", `{}`))

		if rr.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rr.Code)
		}
	})
}

func TestHandleRevokeKey(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"revokes", nil, http.StatusNoContent},
		{"already revoked or unknown", domain.ErrNotFound, http.StatusNotFound},
		{"storage failure", errors.New("db down"), http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := newAdminMux(nil, nil, &mockKeyRevoker{err: tt.err})

			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, adminRequest(http.MethodDelete, "/admin/keys/key-1", ""))

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
		})
	}
}
