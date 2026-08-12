package handler

import (
	"errors"
	"net/http"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

type CertificateHandler struct {
	certify Certifier
	verify  Verifier
	delete  Deleter
}

func NewCertificateHandler(certify Certifier, verify Verifier, delete Deleter) *CertificateHandler {
	return &CertificateHandler{certify: certify, verify: verify, delete: delete}
}

// RegisterRoutes mounts the certificate endpoints. admin wraps the routes that
// mutate or expose the registry beyond public verification; pass
// handler.AdminAuth(token) in production. A nil admin leaves those routes
// unguarded and is intended only for tests that exercise the handlers directly.
func (h *CertificateHandler) RegisterRoutes(mux *http.ServeMux, admin func(http.Handler) http.Handler) {
	guard := orIdentity(admin)

	mux.HandleFunc("POST /certificates", h.handleCertify)
	mux.HandleFunc("GET /certificates/verify", h.handleVerifyByHash)
	mux.HandleFunc("POST /certificates/verify", h.handleVerifyByFile)
	mux.Handle("DELETE /certificates/{hash}", guard(http.HandlerFunc(h.handleDelete)))
}

// orIdentity substitutes a pass-through for a nil middleware so callers can
// omit a guard without every registration site branching.
func orIdentity(mw func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	if mw == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return mw
}

func (h *CertificateHandler) handleCertify(w http.ResponseWriter, r *http.Request) {
	file, ok := parseMediaUpload(w, r)
	if !ok {
		return
	}
	defer file.Close()

	out, err := h.certify.Execute(r.Context(), usecase.CertifyInput{
		Content:    file,
		Registrant: r.Header.Get("X-Registrant"),
	})
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, domain.ErrAlreadyCertified) {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, toCertDTO(out.Certificate))
}

func (h *CertificateHandler) handleVerifyByHash(w http.ResponseWriter, r *http.Request) {
	hash := r.URL.Query().Get("hash")
	if hash == "" {
		writeError(w, http.StatusBadRequest, "query parameter 'hash' is required")
		return
	}

	out, err := h.verify.Execute(r.Context(), usecase.VerifyInput{Hash: hash})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeVerifyResponse(w, out)
}

func (h *CertificateHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	// The {hash} route segment is always non-empty when matched by the mux,
	// so no emptiness check is needed here; the use case guards it anyway.
	err := h.delete.Execute(r.Context(), usecase.DeleteInput{Hash: r.PathValue("hash")})
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, domain.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *CertificateHandler) handleVerifyByFile(w http.ResponseWriter, r *http.Request) {
	file, ok := parseMediaUpload(w, r)
	if !ok {
		return
	}
	defer file.Close()

	out, err := h.verify.Execute(r.Context(), usecase.VerifyInput{Content: file})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeVerifyResponse(w, out)
}
