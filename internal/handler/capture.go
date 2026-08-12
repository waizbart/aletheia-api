package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/waizbart/aletheia-api/internal/attestation"
	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

// Ports the capture surface depends on.
type (
	NonceIssuer interface {
		Execute(ctx context.Context, orgID string) (*domain.CaptureNonce, error)
	}
	DeviceEnroller interface {
		Execute(ctx context.Context, in usecase.EnrollDeviceInput) (*domain.Device, error)
	}
	DeviceRevoker interface {
		Execute(ctx context.Context, in usecase.RevokeDeviceInput) error
	}
	AttestedCapturer interface {
		Execute(ctx context.Context, in usecase.AttestedCaptureInput) (*usecase.CertifyOutput, error)
	}
	UsageReporter interface {
		Summary(ctx context.Context, org *domain.Org) (*usecase.UsageReport, error)
	}
)

// CaptureHandler exposes the gated certification path: challenge, enrol,
// capture.
type CaptureHandler struct {
	nonces   NonceIssuer
	enroll   DeviceEnroller
	revoke   DeviceRevoker
	capture  AttestedCapturer
	usage    UsageReporter
	quota    QuotaChecker
	maxChain int
}

func NewCaptureHandler(
	nonces NonceIssuer,
	enroll DeviceEnroller,
	revoke DeviceRevoker,
	capture AttestedCapturer,
	usage UsageReporter,
	quota QuotaChecker,
) *CaptureHandler {
	return &CaptureHandler{
		nonces:  nonces,
		enroll:  enroll,
		revoke:  revoke,
		capture: capture,
		usage:   usage,
		quota:   quota,
		// An attestation chain is a handful of certificates. Bounding it stops
		// a caller making the server parse an arbitrarily long list.
		maxChain: 10,
	}
}

// RegisterRoutes mounts the tenant-authenticated capture surface. Every route
// here requires an API key, so they are registered on a private mux mounted
// once behind the auth middleware.
func (h *CaptureHandler) RegisterRoutes(mux *http.ServeMux, auth func(http.Handler) http.Handler) {
	inner := http.NewServeMux()

	inner.HandleFunc("POST /captures/nonce", h.handleNonce)
	inner.HandleFunc("POST /captures", h.handleCapture)
	inner.HandleFunc("POST /devices", h.handleEnroll)
	inner.HandleFunc("POST /devices/{id}/revoke", h.handleRevoke)
	inner.HandleFunc("GET /usage", h.handleUsage)

	guarded := orIdentity(auth)(inner)
	for _, pattern := range []string{
		"POST /captures/nonce",
		"POST /captures",
		"POST /devices",
		"POST /devices/{id}/revoke",
		"GET /usage",
	} {
		mux.Handle(pattern, guarded)
	}
}

type nonceDTO struct {
	Nonce     string `json:"nonce"`
	ExpiresAt string `json:"expires_at"`
}

func (h *CaptureHandler) handleNonce(w http.ResponseWriter, r *http.Request) {
	org := OrgFromContext(r.Context())

	n, err := h.nonces.Execute(r.Context(), org.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not issue a challenge")
		return
	}

	writeJSON(w, http.StatusCreated, nonceDTO{
		Nonce:     n.Value,
		ExpiresAt: n.ExpiresAt.Format(time.RFC3339),
	})
}

type enrollRequest struct {
	Platform string `json:"platform"`
	Nonce    string `json:"nonce"`
	// CertChain is the attestation chain, leaf first, each entry standard
	// base64-encoded DER.
	CertChain []string `json:"cert_chain"`
	Model     string   `json:"model"`
}

type deviceDTO struct {
	ID               string `json:"id"`
	Platform         string `json:"platform"`
	AttestationLevel string `json:"attestation_level"`
	Model            string `json:"model"`
	Status           string `json:"status"`
	CreatedAt        string `json:"created_at"`
}

func toDeviceDTO(d *domain.Device) deviceDTO {
	return deviceDTO{
		ID:               d.ID,
		Platform:         string(d.Platform),
		AttestationLevel: string(d.AttestationLevel),
		Model:            d.Model,
		Status:           string(d.Status),
		CreatedAt:        d.CreatedAt.Format(time.RFC3339),
	}
}

func (h *CaptureHandler) handleEnroll(w http.ResponseWriter, r *http.Request) {
	org := OrgFromContext(r.Context())

	var req enrollRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be JSON")
		return
	}
	if len(req.CertChain) == 0 || len(req.CertChain) > h.maxChain {
		writeError(w, http.StatusBadRequest, "cert_chain must hold between 1 and 10 certificates")
		return
	}

	chain := make([][]byte, 0, len(req.CertChain))
	for _, encoded := range req.CertChain {
		der, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			writeError(w, http.StatusBadRequest, "cert_chain entries must be base64-encoded DER")
			return
		}
		chain = append(chain, der)
	}

	device, err := h.enroll.Execute(r.Context(), usecase.EnrollDeviceInput{
		OrgID:     org.ID,
		Platform:  domain.Platform(req.Platform),
		Nonce:     req.Nonce,
		CertChain: chain,
		Model:     req.Model,
	})
	if err != nil {
		writeEnrollError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, toDeviceDTO(device))
}

// writeEnrollError separates "your device is not trusted" from "we broke".
func writeEnrollError(w http.ResponseWriter, err error) {
	var rejected *attestation.ErrRejected
	switch {
	case errors.As(err, &rejected):
		// The request was well-formed; the device just is not trusted. The
		// reason is returned verbatim because an integrator debugging their
		// SDK needs to know which gate they failed.
		writeError(w, http.StatusForbidden, rejected.Error())
	case errors.Is(err, attestation.ErrUnsupportedPlatform):
		writeError(w, http.StatusNotImplemented, err.Error())
	case errors.Is(err, domain.ErrNonceUnusable):
		writeError(w, http.StatusConflict, "capture challenge is unknown, expired or already used")
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

func (h *CaptureHandler) handleRevoke(w http.ResponseWriter, r *http.Request) {
	org := OrgFromContext(r.Context())

	var req struct {
		Reason string `json:"reason"`
	}
	// A missing or empty body is fine: the reason is optional.
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody)).Decode(&req)

	err := h.revoke.Execute(r.Context(), usecase.RevokeDeviceInput{
		OrgID:    org.ID,
		DeviceID: r.PathValue("id"),
		Reason:   req.Reason,
	})
	if errors.Is(err, domain.ErrDeviceNotFound) {
		writeError(w, http.StatusNotFound, "device not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not revoke the device")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *CaptureHandler) handleCapture(w http.ResponseWriter, r *http.Request) {
	org := OrgFromContext(r.Context())

	if err := h.quota.Check(r.Context(), org, domain.OpAttestedCapture); err != nil {
		if errors.Is(err, domain.ErrQuotaExceeded) {
			writeError(w, http.StatusPaymentRequired, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "could not check the plan allowance")
		return
	}

	file, ok := parseMediaUpload(w, r)
	if !ok {
		return
	}
	defer file.Close()

	signature, err := base64.StdEncoding.DecodeString(r.FormValue("signature"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "signature must be base64-encoded")
		return
	}

	capturedAt, err := time.Parse(time.RFC3339Nano, r.FormValue("captured_at"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "captured_at must be an RFC 3339 timestamp")
		return
	}

	out, err := h.capture.Execute(r.Context(), usecase.AttestedCaptureInput{
		OrgID:     org.ID,
		DeviceID:  r.FormValue("device_id"),
		Nonce:     r.FormValue("nonce"),
		Signature: signature,
		Metadata: domain.CaptureMetadata{
			CapturedAt: capturedAt,
			Model:      r.FormValue("model"),
			OSVersion:  r.FormValue("os_version"),
			AppVersion: r.FormValue("app_version"),
		},
		Content: file,
	})
	if err != nil {
		writeCaptureError(w, err)
		return
	}

	// Counted only now, so a rejected capture is never billed.
	if err := h.quota.Record(r.Context(), org.ID, domain.OpAttestedCapture); err != nil {
		// The capture is certified; losing a usage count must not fail it.
		logUsageFailure(err)
	}

	writeJSON(w, http.StatusCreated, toCertDTO(out.Certificate))
}

func writeCaptureError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrCaptureSignature):
		writeError(w, http.StatusForbidden, "capture signature does not verify against the enrolled device key")
	case errors.Is(err, domain.ErrNonceUnusable):
		writeError(w, http.StatusConflict, "capture challenge is unknown, expired or already used")
	case errors.Is(err, domain.ErrDeviceRevoked):
		writeError(w, http.StatusForbidden, "device is revoked")
	case errors.Is(err, domain.ErrDeviceNotFound):
		writeError(w, http.StatusNotFound, "device not enrolled")
	case errors.Is(err, domain.ErrAlreadyCertified):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	}
}

type usageDTO struct {
	Period     string         `json:"period"`
	Plan       string         `json:"plan"`
	Operations []usageLineDTO `json:"operations"`
}

type usageLineDTO struct {
	Operation string `json:"operation"`
	Used      int    `json:"used"`
	// Limit is null when the plan does not cap the operation.
	Limit *int `json:"limit"`
}

func (h *CaptureHandler) handleUsage(w http.ResponseWriter, r *http.Request) {
	org := OrgFromContext(r.Context())

	report, err := h.usage.Summary(r.Context(), org)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read usage")
		return
	}

	dto := usageDTO{Period: report.Period, Plan: string(report.Plan)}
	for _, line := range report.Operations {
		l := usageLineDTO{Operation: string(line.Operation), Used: line.Used}
		if line.Limit != domain.Unlimited {
			limit := line.Limit
			l.Limit = &limit
		}
		dto.Operations = append(dto.Operations, l)
	}

	writeJSON(w, http.StatusOK, dto)
}
