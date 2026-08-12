package handler_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/waizbart/aletheia-api/internal/attestation"
	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/handler"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

// --- mocks ------------------------------------------------------------------

type mockAuthenticator struct {
	fn func(ctx context.Context, plaintext string) (*domain.Org, error)
}

func (m *mockAuthenticator) Execute(ctx context.Context, plaintext string) (*domain.Org, error) {
	return m.fn(ctx, plaintext)
}

type mockQuota struct {
	checkErr error
	recorded []domain.Operation
	recordFn func(ctx context.Context, orgID string, op domain.Operation) error
}

func (m *mockQuota) Check(context.Context, *domain.Org, domain.Operation) error { return m.checkErr }

func (m *mockQuota) Record(ctx context.Context, orgID string, op domain.Operation) error {
	m.recorded = append(m.recorded, op)
	if m.recordFn == nil {
		return nil
	}
	return m.recordFn(ctx, orgID, op)
}

type mockNonceIssuer struct {
	fn func(ctx context.Context, orgID string) (*domain.CaptureNonce, error)
}

func (m *mockNonceIssuer) Execute(ctx context.Context, orgID string) (*domain.CaptureNonce, error) {
	if m.fn == nil {
		return &domain.CaptureNonce{Value: strings.Repeat("a", 64), ExpiresAt: time.Now().Add(time.Minute)}, nil
	}
	return m.fn(ctx, orgID)
}

type mockEnroller struct {
	fn func(ctx context.Context, in usecase.EnrollDeviceInput) (*domain.Device, error)
}

func (m *mockEnroller) Execute(ctx context.Context, in usecase.EnrollDeviceInput) (*domain.Device, error) {
	if m.fn == nil {
		return &domain.Device{ID: "device-1", Platform: in.Platform, Status: domain.DeviceActive}, nil
	}
	return m.fn(ctx, in)
}

type mockDeviceRevoker struct {
	err error
}

func (m *mockDeviceRevoker) Execute(context.Context, usecase.RevokeDeviceInput) error { return m.err }

type mockCapturer struct {
	fn func(ctx context.Context, in usecase.AttestedCaptureInput) (*usecase.CertifyOutput, error)
}

func (m *mockCapturer) Execute(ctx context.Context, in usecase.AttestedCaptureInput) (*usecase.CertifyOutput, error) {
	if m.fn == nil {
		return &usecase.CertifyOutput{Certificate: &domain.Certificate{
			ID: "cert-1", ContentHash: strings.Repeat("a", 64), DeviceID: "device-1", CreatedAt: time.Now(),
		}}, nil
	}
	return m.fn(ctx, in)
}

type mockUsageReporter struct {
	fn func(ctx context.Context, org *domain.Org) (*usecase.UsageReport, error)
}

func (m *mockUsageReporter) Summary(ctx context.Context, org *domain.Org) (*usecase.UsageReport, error) {
	if m.fn == nil {
		return &usecase.UsageReport{
			Period: "2026-08",
			Plan:   org.Plan,
			Operations: []usecase.UsageLine{
				{Operation: domain.OpAttestedCapture, Used: 3, Limit: 500},
				{Operation: domain.OpVerify, Used: 1, Limit: domain.Unlimited},
			},
		}, nil
	}
	return m.fn(ctx, org)
}

// --- helpers ----------------------------------------------------------------

const testAPIKey = "alk_0123456789abcdefghijklmnop"

func testOrg() *domain.Org {
	return &domain.Org{ID: "org-1", Name: "Acme", Plan: domain.PlanDeveloper, Status: domain.OrgActive}
}

func okAuthenticator() *mockAuthenticator {
	return &mockAuthenticator{fn: func(_ context.Context, plaintext string) (*domain.Org, error) {
		if plaintext != testAPIKey {
			return nil, domain.ErrUnauthorized
		}
		return testOrg(), nil
	}}
}

// captureRequest builds the multipart body the SDK sends.
func captureRequest(t *testing.T, fields map[string]string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="file"; filename="capture.jpg"`)
	h.Set("Content-Type", "image/jpeg")
	pw, err := mw.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(pw, "image bytes")

	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/captures", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	return req
}

func validCaptureFields() map[string]string {
	return map[string]string{
		"device_id":   "device-1",
		"nonce":       strings.Repeat("a", 64),
		"signature":   base64.StdEncoding.EncodeToString([]byte("signature")),
		"captured_at": time.Now().UTC().Format(time.RFC3339Nano),
		"model":       "Pixel 8",
		"os_version":  "14",
		"app_version": "1.0.0",
	}
}

// --- API key authentication -------------------------------------------------

func TestAPIKeyAuth(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		auth       *mockAuthenticator
		wantStatus int
	}{
		{
			name:       "valid credential passes",
			header:     "Bearer " + testAPIKey,
			auth:       okAuthenticator(),
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing header",
			auth:       okAuthenticator(),
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "unknown credential",
			header:     "Bearer alk_wrongwrongwrongwrongwrong",
			auth:       okAuthenticator(),
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:   "storage failure is ours, not the caller's",
			header: "Bearer " + testAPIKey,
			auth: &mockAuthenticator{fn: func(context.Context, string) (*domain.Org, error) {
				return nil, errors.New("db down")
			}},
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotOrg *domain.Org
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotOrg = handler.OrgFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			})

			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/usage", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			handler.APIKeyAuth(tt.auth)(inner).ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusOK && (gotOrg == nil || gotOrg.ID != "org-1") {
				t.Error("the authenticated org must reach the handler")
			}
		})
	}
}

// TestOptionalAPIKeyAuth pins the free tier: an absent or bad credential must
// not block a public verification.
func TestOptionalAPIKeyAuth(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		wantOrg bool
	}{
		{name: "anonymous caller passes through", wantOrg: false},
		{name: "bad credential still passes through", header: "Bearer alk_nope-nope-nope-nope", wantOrg: false},
		{name: "valid credential attaches the org", header: "Bearer " + testAPIKey, wantOrg: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotOrg *domain.Org
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotOrg = handler.OrgFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			})

			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/certificates/verify", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			handler.OptionalAPIKeyAuth(okAuthenticator())(inner).ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 — public verification must never be blocked", rr.Code)
			}
			if got := gotOrg != nil; got != tt.wantOrg {
				t.Errorf("org attached = %v, want %v", got, tt.wantOrg)
			}
		})
	}
}

func TestOrgFromContext_Absent(t *testing.T) {
	if handler.OrgFromContext(context.Background()) != nil {
		t.Error("a bare context carries no org")
	}
}

// --- capture surface --------------------------------------------------------

func newCaptureMux(t *testing.T, capturer *mockCapturer, quota *mockQuota, enroller *mockEnroller, revoker *mockDeviceRevoker) *http.ServeMux {
	t.Helper()
	if capturer == nil {
		capturer = &mockCapturer{}
	}
	if quota == nil {
		quota = &mockQuota{}
	}
	if enroller == nil {
		enroller = &mockEnroller{}
	}
	if revoker == nil {
		revoker = &mockDeviceRevoker{}
	}

	h := handler.NewCaptureHandler(&mockNonceIssuer{}, enroller, revoker, capturer, &mockUsageReporter{}, quota)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, handler.APIKeyAuth(okAuthenticator()))
	return mux
}

func TestCaptureRoutes_RequireAPIKey(t *testing.T) {
	mux := newCaptureMux(t, nil, nil, nil, nil)

	for _, route := range []struct{ method, target string }{
		{http.MethodPost, "/captures/nonce"},
		{http.MethodPost, "/captures"},
		{http.MethodPost, "/devices"},
		{http.MethodPost, "/devices/device-1/revoke"},
		{http.MethodGet, "/usage"},
	} {
		t.Run(route.method+" "+route.target, func(t *testing.T) {
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, httptest.NewRequest(route.method, route.target, nil))
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rr.Code)
			}
		})
	}
}

func TestHandleNonce(t *testing.T) {
	mux := newCaptureMux(t, nil, nil, nil, nil)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/captures/nonce", nil)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rr.Code)
	}
	var body struct {
		Nonce     string `json:"nonce"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Nonce == "" || body.ExpiresAt == "" {
		t.Errorf("incomplete response: %+v", body)
	}
}

func TestHandleCapture_Success(t *testing.T) {
	quota := &mockQuota{}
	mux := newCaptureMux(t, nil, quota, nil, nil)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, captureRequest(t, validCaptureFields()))

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rr.Code, rr.Body)
	}

	var dto map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if dto["attested"] != true {
		t.Error("a capture certificate must report as attested")
	}

	if len(quota.recorded) != 1 || quota.recorded[0] != domain.OpAttestedCapture {
		t.Errorf("recorded = %v, want one attested_capture", quota.recorded)
	}
}

func TestHandleCapture_QuotaExceeded(t *testing.T) {
	quota := &mockQuota{checkErr: fmt.Errorf("quota check: %w: spent", domain.ErrQuotaExceeded)}
	mux := newCaptureMux(t, nil, quota, nil, nil)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, captureRequest(t, validCaptureFields()))

	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", rr.Code)
	}
	if len(quota.recorded) != 0 {
		t.Error("a refused capture must not be billed")
	}
}

func TestHandleCapture_QuotaLookupFailure(t *testing.T) {
	quota := &mockQuota{checkErr: errors.New("db down")}
	mux := newCaptureMux(t, nil, quota, nil, nil)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, captureRequest(t, validCaptureFields()))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
}

// TestHandleCapture_NotBilledOnFailure is the property a customer would notice
// first: a rejected capture must never appear on an invoice.
func TestHandleCapture_NotBilledOnFailure(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"bad signature", domain.ErrCaptureSignature, http.StatusForbidden},
		{"spent nonce", domain.ErrNonceUnusable, http.StatusConflict},
		{"revoked device", domain.ErrDeviceRevoked, http.StatusForbidden},
		{"unknown device", domain.ErrDeviceNotFound, http.StatusNotFound},
		{"duplicate content", domain.ErrAlreadyCertified, http.StatusConflict},
		{"anything else", errors.New("boom"), http.StatusUnprocessableEntity},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quota := &mockQuota{}
			capturer := &mockCapturer{fn: func(context.Context, usecase.AttestedCaptureInput) (*usecase.CertifyOutput, error) {
				return nil, fmt.Errorf("capture: %w", tt.err)
			}}
			mux := newCaptureMux(t, capturer, quota, nil, nil)

			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, captureRequest(t, validCaptureFields()))

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if len(quota.recorded) != 0 {
				t.Error("a failed capture must not be billed")
			}
		})
	}
}

func TestHandleCapture_MalformedFields(t *testing.T) {
	tests := []struct {
		name  string
		field string
		value string
	}{
		{"signature is not base64", "signature", "!!!not base64!!!"},
		{"captured_at is not a timestamp", "captured_at", "yesterday"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := validCaptureFields()
			fields[tt.field] = tt.value
			mux := newCaptureMux(t, nil, nil, nil, nil)

			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, captureRequest(t, fields))

			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rr.Code)
			}
		})
	}
}

// TestHandleCapture_BillingFailureDoesNotFailTheCapture: the capture is already
// certified by then, so losing a usage count is a billing problem, not a
// customer-facing one.
func TestHandleCapture_BillingFailureDoesNotFailTheCapture(t *testing.T) {
	quota := &mockQuota{recordFn: func(context.Context, string, domain.Operation) error {
		return errors.New("counter unavailable")
	}}
	mux := newCaptureMux(t, nil, quota, nil, nil)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, captureRequest(t, validCaptureFields()))

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rr.Code)
	}
}

// --- device enrolment -------------------------------------------------------

func enrollRequestBody(t *testing.T, body map[string]any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/devices", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	return req
}

func TestHandleEnroll(t *testing.T) {
	validBody := map[string]any{
		"platform":   "android",
		"nonce":      strings.Repeat("a", 64),
		"cert_chain": []string{base64.StdEncoding.EncodeToString([]byte("leaf"))},
		"model":      "Pixel 8",
	}

	t.Run("enrols a trusted device", func(t *testing.T) {
		mux := newCaptureMux(t, nil, nil, nil, nil)

		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, enrollRequestBody(t, validBody))

		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", rr.Code, rr.Body)
		}
	})

	t.Run("reports an untrusted device as forbidden, not as a server error", func(t *testing.T) {
		enroller := &mockEnroller{fn: func(context.Context, usecase.EnrollDeviceInput) (*domain.Device, error) {
			return nil, fmt.Errorf("enroll: %w", &attestation.ErrRejected{Reason: "device bootloader is unlocked"})
		}}
		mux := newCaptureMux(t, nil, nil, enroller, nil)

		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, enrollRequestBody(t, validBody))

		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rr.Code)
		}
		// An integrator debugging their SDK needs to know which gate failed.
		if !strings.Contains(rr.Body.String(), "bootloader is unlocked") {
			t.Errorf("response %s should name the failed gate", rr.Body)
		}
	})

	t.Run("reports an unsupported platform as not implemented", func(t *testing.T) {
		enroller := &mockEnroller{fn: func(context.Context, usecase.EnrollDeviceInput) (*domain.Device, error) {
			return nil, fmt.Errorf("enroll: %w: ios", attestation.ErrUnsupportedPlatform)
		}}
		mux := newCaptureMux(t, nil, nil, enroller, nil)

		body := map[string]any{
			"platform":   "ios",
			"nonce":      strings.Repeat("a", 64),
			"cert_chain": []string{base64.StdEncoding.EncodeToString([]byte("leaf"))},
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, enrollRequestBody(t, body))

		if rr.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want 501", rr.Code)
		}
	})

	t.Run("reports a spent challenge as a conflict", func(t *testing.T) {
		enroller := &mockEnroller{fn: func(context.Context, usecase.EnrollDeviceInput) (*domain.Device, error) {
			return nil, fmt.Errorf("enroll: %w", domain.ErrNonceUnusable)
		}}
		mux := newCaptureMux(t, nil, nil, enroller, nil)

		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, enrollRequestBody(t, validBody))

		if rr.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rr.Code)
		}
	})

	t.Run("rejects malformed requests", func(t *testing.T) {
		longChain := make([]string, 11)
		for i := range longChain {
			longChain[i] = base64.StdEncoding.EncodeToString([]byte("cert"))
		}

		cases := map[string]map[string]any{
			"empty chain":         {"platform": "android", "nonce": strings.Repeat("a", 64), "cert_chain": []string{}},
			"oversized chain":     {"platform": "android", "nonce": strings.Repeat("a", 64), "cert_chain": longChain},
			"chain is not base64": {"platform": "android", "nonce": strings.Repeat("a", 64), "cert_chain": []string{"!!!"}},
		}

		for name, body := range cases {
			t.Run(name, func(t *testing.T) {
				mux := newCaptureMux(t, nil, nil, nil, nil)
				rr := httptest.NewRecorder()
				mux.ServeHTTP(rr, enrollRequestBody(t, body))
				if rr.Code != http.StatusBadRequest {
					t.Errorf("status = %d, want 400", rr.Code)
				}
			})
		}
	})

	t.Run("rejects a non-JSON body", func(t *testing.T) {
		mux := newCaptureMux(t, nil, nil, nil, nil)

		req := httptest.NewRequest(http.MethodPost, "/devices", strings.NewReader("not json"))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rr.Code)
		}
	})
}

func TestHandleRevokeDevice(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"revokes", nil, http.StatusNoContent},
		{"unknown device", fmt.Errorf("revoke device: %w", domain.ErrDeviceNotFound), http.StatusNotFound},
		{"storage failure", errors.New("db down"), http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := newCaptureMux(t, nil, nil, nil, &mockDeviceRevoker{err: tt.err})

			req := httptest.NewRequest(http.MethodPost, "/devices/device-1/revoke",
				strings.NewReader(`{"reason":"lost"}`))
			req.Header.Set("Authorization", "Bearer "+testAPIKey)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
		})
	}
}

func TestHandleUsage(t *testing.T) {
	mux := newCaptureMux(t, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/usage", nil)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var body struct {
		Period     string `json:"period"`
		Plan       string `json:"plan"`
		Operations []struct {
			Operation string `json:"operation"`
			Used      int    `json:"used"`
			Limit     *int   `json:"limit"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Operations) != 2 {
		t.Fatalf("got %d operation lines", len(body.Operations))
	}
	if body.Operations[0].Limit == nil || *body.Operations[0].Limit != 500 {
		t.Error("a capped operation should report its limit")
	}
	// An uncapped operation reports null rather than 0, which would read as
	// "no allowance at all".
	if body.Operations[1].Limit != nil {
		t.Errorf("an uncapped operation should report a null limit, got %v", *body.Operations[1].Limit)
	}
}

func TestHandleUsage_Failure(t *testing.T) {
	reporter := &mockUsageReporter{fn: func(context.Context, *domain.Org) (*usecase.UsageReport, error) {
		return nil, errors.New("db down")
	}}
	h := handler.NewCaptureHandler(&mockNonceIssuer{}, &mockEnroller{}, &mockDeviceRevoker{}, &mockCapturer{}, reporter, &mockQuota{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, handler.APIKeyAuth(okAuthenticator()))

	req := httptest.NewRequest(http.MethodGet, "/usage", nil)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
}

func TestHandleNonce_Failure(t *testing.T) {
	issuer := &mockNonceIssuer{fn: func(context.Context, string) (*domain.CaptureNonce, error) {
		return nil, errors.New("db down")
	}}
	h := handler.NewCaptureHandler(issuer, &mockEnroller{}, &mockDeviceRevoker{}, &mockCapturer{}, &mockUsageReporter{}, &mockQuota{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, handler.APIKeyAuth(okAuthenticator()))

	req := httptest.NewRequest(http.MethodPost, "/captures/nonce", nil)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
}

// --- legacy upload path -----------------------------------------------------

// TestLegacyCertify_DisabledByDefault pins the default that makes the product
// claim true: an upload nobody vouched for does not become a certificate.
func TestLegacyCertify_DisabledByDefault(t *testing.T) {
	h := handler.NewCertificateHandler(&mockCertifier{}, &mockVerifier{}, &mockDeleter{}, &mockQuota{}, false)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, nil, nil)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, newUploadRequest(t, http.MethodPost, "/certificates", "image/jpeg", []byte("data")))

	if rr.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "/captures") {
		t.Error("the response should point at the supported path")
	}
}

func TestVerify_MeteredOnlyWhenAuthenticated(t *testing.T) {
	verifier := &mockVerifier{executeFn: func(context.Context, usecase.VerifyInput) (*usecase.VerifyOutput, error) {
		return &usecase.VerifyOutput{Certified: false}, nil
	}}

	t.Run("anonymous verification is free", func(t *testing.T) {
		quota := &mockQuota{}
		h := handler.NewCertificateHandler(&mockCertifier{}, verifier, &mockDeleter{}, quota, false)
		mux := http.NewServeMux()
		h.RegisterRoutes(mux, nil, nil)

		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/certificates/verify?hash="+strings.Repeat("a", 64), nil))

		if rr.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (reachable, not certified)", rr.Code)
		}
		if len(quota.recorded) != 0 {
			t.Error("anonymous verification must not be metered")
		}
	})

	t.Run("authenticated verification is metered", func(t *testing.T) {
		quota := &mockQuota{}
		h := handler.NewCertificateHandler(&mockCertifier{}, verifier, &mockDeleter{}, quota, false)
		mux := http.NewServeMux()
		h.RegisterRoutes(mux, nil, nil)

		wrapped := handler.OptionalAPIKeyAuth(okAuthenticator())(mux)

		req := httptest.NewRequest(http.MethodGet, "/certificates/verify?hash="+strings.Repeat("a", 64), nil)
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)

		if len(quota.recorded) != 1 || quota.recorded[0] != domain.OpVerify {
			t.Errorf("recorded = %v, want one verify", quota.recorded)
		}
	})
}

func TestWriteEnrollError_UnclassifiedFailure(t *testing.T) {
	enroller := &mockEnroller{fn: func(context.Context, usecase.EnrollDeviceInput) (*domain.Device, error) {
		return nil, errors.New("enroll: unsupported platform \"symbian\"")
	}}
	mux := newCaptureMux(t, nil, nil, enroller, nil)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, enrollRequestBody(t, map[string]any{
		"platform":   "symbian",
		"nonce":      strings.Repeat("a", 64),
		"cert_chain": []string{base64.StdEncoding.EncodeToString([]byte("leaf"))},
	}))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestLegacyCertify_WhenEnabled(t *testing.T) {
	captured := make(chan usecase.CertifyInput, 1)
	certifier := &mockCertifier{executeFn: func(_ context.Context, in usecase.CertifyInput) (*usecase.CertifyOutput, error) {
		captured <- in
		return &usecase.CertifyOutput{Certificate: &domain.Certificate{
			ID: "cert-1", ContentHash: strings.Repeat("b", 64), CreatedAt: time.Now(),
		}}, nil
	}}

	h := handler.NewCertificateHandler(certifier, &mockVerifier{}, &mockDeleter{}, &mockQuota{}, true)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, nil, handler.APIKeyAuth(okAuthenticator()))

	req := newUploadRequest(t, http.MethodPost, "/certificates", "image/jpeg", []byte("data"))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	req.Header.Set("X-Registrant", "whatever-the-caller-claims")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rr.Code, rr.Body)
	}

	in := <-captured
	// The authenticated tenant is the registrant of record; the header has
	// never been evidence of anything.
	if in.Registrant != "org-1" || in.OrgID != "org-1" {
		t.Errorf("registrant = %q, org = %q — want the authenticated org", in.Registrant, in.OrgID)
	}

	var dto map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if dto["attested"] != false {
		t.Error("an unattested upload must not claim to be attested")
	}
}

func TestLegacyCertify_AnonymousKeepsTheHeaderLabel(t *testing.T) {
	captured := make(chan usecase.CertifyInput, 1)
	certifier := &mockCertifier{executeFn: func(_ context.Context, in usecase.CertifyInput) (*usecase.CertifyOutput, error) {
		captured <- in
		return nil, domain.ErrAlreadyCertified
	}}

	h := handler.NewCertificateHandler(certifier, &mockVerifier{}, &mockDeleter{}, nil, true)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, nil, nil)

	req := newUploadRequest(t, http.MethodPost, "/certificates", "image/jpeg", []byte("data"))
	req.Header.Set("X-Registrant", "self-declared")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
	if in := <-captured; in.Registrant != "self-declared" || in.OrgID != "" {
		t.Errorf("registrant = %q, org = %q", in.Registrant, in.OrgID)
	}
}

func TestVerify_NoQuotaCheckerConfigured(t *testing.T) {
	verifier := &mockVerifier{executeFn: func(context.Context, usecase.VerifyInput) (*usecase.VerifyOutput, error) {
		return &usecase.VerifyOutput{Certified: true, Certificate: &domain.Certificate{
			ID: "cert-1", ContentHash: strings.Repeat("c", 64), CreatedAt: time.Now(),
		}}, nil
	}}
	h := handler.NewCertificateHandler(&mockCertifier{}, verifier, &mockDeleter{}, nil, false)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, nil, nil)

	wrapped := handler.OptionalAPIKeyAuth(okAuthenticator())(mux)

	req := httptest.NewRequest(http.MethodGet, "/certificates/verify?hash="+strings.Repeat("c", 64), nil)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

func TestCertDTO_IncludesCaptureProvenance(t *testing.T) {
	capturedAt := time.Date(2026, 8, 12, 10, 30, 0, 0, time.UTC)
	verifier := &mockVerifier{executeFn: func(context.Context, usecase.VerifyInput) (*usecase.VerifyOutput, error) {
		return &usecase.VerifyOutput{Certified: true, Certificate: &domain.Certificate{
			ID: "cert-1", ContentHash: strings.Repeat("d", 64),
			DeviceID: "device-1", CapturedAt: &capturedAt, CreatedAt: capturedAt,
		}}, nil
	}}
	h := handler.NewCertificateHandler(&mockCertifier{}, verifier, &mockDeleter{}, nil, false)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, nil, nil)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/certificates/verify?hash="+strings.Repeat("d", 64), nil))

	var body struct {
		Certificate struct {
			Attested   bool   `json:"attested"`
			DeviceID   string `json:"device_id"`
			CapturedAt string `json:"captured_at"`
		} `json:"certificate"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Certificate.Attested || body.Certificate.DeviceID != "device-1" {
		t.Errorf("provenance missing from the response: %+v", body.Certificate)
	}
	if body.Certificate.CapturedAt == "" {
		t.Error("captured_at should be exposed for attested certificates")
	}
}
