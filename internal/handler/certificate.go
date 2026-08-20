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
	quota   QuotaChecker

	// allowUnattested keeps the pre-attestation upload path open. It defaults
	// off: an upload nobody vouched for is exactly what attested capture
	// exists to replace, and leaving it on would let a tenant mint
	// certificates that look official but prove nothing about origin.
	allowUnattested bool
}

func NewCertificateHandler(certify Certifier, verify Verifier, delete Deleter, quota QuotaChecker, allowUnattested bool) *CertificateHandler {
	return &CertificateHandler{
		certify:         certify,
		verify:          verify,
		delete:          delete,
		quota:           quota,
		allowUnattested: allowUnattested,
	}
}

// RegisterRoutes mounts the certificate endpoints.
//
// admin guards deletion; tenant authenticates the legacy upload path. Verify
// stays reachable anonymously — it is the free half of the product — and is
// wrapped by the caller in OptionalAPIKeyAuth so authenticated verification can
// still be metered. Passing nil for either middleware leaves those routes
// unguarded and is intended only for tests.
func (h *CertificateHandler) RegisterRoutes(mux *http.ServeMux, admin, tenant func(http.Handler) http.Handler) {
	mux.Handle("POST /certificates", orIdentity(tenant)(http.HandlerFunc(h.handleCertify)))
	mux.HandleFunc("GET /certificates/verify", h.handleVerifyByHash)
	mux.HandleFunc("POST /certificates/verify", h.handleVerifyByFile)
	mux.Handle("DELETE /certificates/{hash}", orIdentity(admin)(http.HandlerFunc(h.handleDelete)))
}

// orIdentity substitutes a pass-through for a nil middleware so callers can
// omit a guard without every registration site branching.
func orIdentity(mw func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	if mw == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return mw
}

// handleCertify is the legacy unattested upload path, kept behind a flag as a
// migration route for integrations that predate the SDK.
func (h *CertificateHandler) handleCertify(w http.ResponseWriter, r *http.Request) {
	if !h.allowUnattested {
		writeError(w, http.StatusGone,
			"unattested uploads are disabled; certify through POST /captures with an enrolled device")
		return
	}

	file, ok := parseMediaUpload(w, r)
	if !ok {
		return
	}
	defer file.Close()

	org := OrgFromContext(r.Context())
	registrant := r.Header.Get("X-Registrant")
	orgID := ""
	if org != nil {
		orgID = org.ID
		// The authenticated tenant is the registrant of record. The header is
		// a caller-supplied label and has never been evidence of anything.
		registrant = org.ID
	}

	out, err := h.certify.Execute(r.Context(), usecase.CertifyInput{
		Content:    file,
		Registrant: registrant,
		OrgID:      orgID,
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

	h.recordVerify(r)
	writeVerifyResponse(w, out)
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

	h.recordVerify(r)
	writeVerifyResponse(w, out)
}

// recordVerify meters verification for authenticated callers only. Anonymous
// verification is free and is never counted or capped.
func (h *CertificateHandler) recordVerify(r *http.Request) {
	org := OrgFromContext(r.Context())
	if org == nil || h.quota == nil {
		return
	}
	if err := h.quota.Record(r.Context(), org.ID, domain.OpVerify); err != nil {
		logUsageFailure(err)
	}
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
