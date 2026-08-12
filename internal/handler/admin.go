package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

// Ports the tenant-administration surface depends on.
type (
	OrgCreator interface {
		Execute(ctx context.Context, in usecase.CreateOrgInput) (*domain.Org, error)
	}
	APIKeyIssuer interface {
		Execute(ctx context.Context, orgID, name string) (*usecase.IssueAPIKeyOutput, error)
	}
	APIKeyRevoker interface {
		Execute(ctx context.Context, keyID string) error
	}
)

// AdminHandler manages tenants. Onboarding is deliberately not self-serve: a
// registrant is only worth something if somebody vetted them, so creating an
// org is an operator action behind the admin token.
type AdminHandler struct {
	createOrg OrgCreator
	issueKey  APIKeyIssuer
	revokeKey APIKeyRevoker
}

func NewAdminHandler(createOrg OrgCreator, issueKey APIKeyIssuer, revokeKey APIKeyRevoker) *AdminHandler {
	return &AdminHandler{createOrg: createOrg, issueKey: issueKey, revokeKey: revokeKey}
}

// RegisterRoutes mounts the admin surface behind the admin middleware.
func (h *AdminHandler) RegisterRoutes(mux *http.ServeMux, admin func(http.Handler) http.Handler) {
	inner := http.NewServeMux()
	inner.HandleFunc("POST /admin/orgs", h.handleCreateOrg)
	inner.HandleFunc("POST /admin/orgs/{id}/keys", h.handleIssueKey)
	inner.HandleFunc("DELETE /admin/keys/{id}", h.handleRevokeKey)

	mux.Handle("/admin/", orIdentity(admin)(inner))
}

type orgDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Plan      string `json:"plan"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

func (h *AdminHandler) handleCreateOrg(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Plan string `json:"plan"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be JSON")
		return
	}

	org, err := h.createOrg.Execute(r.Context(), usecase.CreateOrgInput{
		Name: req.Name,
		Plan: domain.Plan(req.Plan),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, orgDTO{
		ID:        org.ID,
		Name:      org.Name,
		Plan:      string(org.Plan),
		Status:    string(org.Status),
		CreatedAt: org.CreatedAt.Format(time.RFC3339),
	})
}

type apiKeyDTO struct {
	ID     string `json:"id"`
	OrgID  string `json:"org_id"`
	Name   string `json:"name"`
	Prefix string `json:"prefix"`
	// Key is the plaintext credential. It is shown exactly once, here — it is
	// not stored and cannot be recovered, only replaced.
	Key       string `json:"key"`
	CreatedAt string `json:"created_at"`
}

func (h *AdminHandler) handleIssueKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody)).Decode(&req)

	out, err := h.issueKey.Execute(r.Context(), r.PathValue("id"), req.Name)
	if errors.Is(err, domain.ErrNotFound) {
		writeError(w, http.StatusNotFound, "organisation not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not issue an API key")
		return
	}

	writeJSON(w, http.StatusCreated, apiKeyDTO{
		ID:        out.Key.ID,
		OrgID:     out.Key.OrgID,
		Name:      out.Key.Name,
		Prefix:    out.Key.Prefix,
		Key:       out.Plaintext,
		CreatedAt: out.Key.CreatedAt.Format(time.RFC3339),
	})
}

func (h *AdminHandler) handleRevokeKey(w http.ResponseWriter, r *http.Request) {
	err := h.revokeKey.Execute(r.Context(), r.PathValue("id"))
	if errors.Is(err, domain.ErrNotFound) {
		writeError(w, http.StatusNotFound, "API key not found or already revoked")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not revoke the API key")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
