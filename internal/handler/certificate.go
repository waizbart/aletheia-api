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
}

func NewCertificateHandler(certify Certifier, verify Verifier) *CertificateHandler {
	return &CertificateHandler{certify: certify, verify: verify}
}

func (h *CertificateHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /certificates", h.handleCertify)
	mux.HandleFunc("GET /certificates/verify", h.handleVerifyByHash)
	mux.HandleFunc("POST /certificates/verify", h.handleVerifyByFile)
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
