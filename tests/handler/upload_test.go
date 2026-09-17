package handler_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/handler"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

func TestParseMediaUpload_MediaKindRouting(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        []byte
		target      string
		wantStatus  int
		wantVideo   bool
	}{
		{
			name:        "image goes to the image pipeline",
			contentType: "image/jpeg",
			body:        []byte("img"),
			target:      "/certificates/verify",
			wantStatus:  http.StatusNotFound, // verifyDTO answers 404 when not certified
		},
		{
			name:        "mp4 goes to the video pipeline",
			contentType: "video/mp4",
			body:        isoBMFFHeader(),
			target:      "/certificates/verify",
			wantStatus:  http.StatusNotFound,
			wantVideo:   true,
		},
		{
			name:        "webm goes to the video pipeline",
			contentType: "video/webm",
			body:        webmHeader(),
			target:      "/certificates/verify",
			wantStatus:  http.StatusNotFound,
			wantVideo:   true,
		},
		{
			name:        "quicktime is recognised through the ftyp box",
			contentType: "video/quicktime",
			body:        isoBMFFHeader(),
			target:      "/certificates/verify",
			wantStatus:  http.StatusNotFound,
			wantVideo:   true,
		},
		{
			// The guard that matters: declaring video and uploading something
			// else is the cheapest way to probe libavcodec.
			name:        "declared video with non-video bytes is refused",
			contentType: "video/mp4",
			body:        []byte("<html><body>not a video at all</body></html>"),
			target:      "/certificates/verify",
			wantStatus:  http.StatusUnsupportedMediaType,
		},
		{
			name:        "an unknown media type is refused",
			contentType: "application/zip",
			body:        []byte("PK\x03\x04"),
			target:      "/certificates/verify",
			wantStatus:  http.StatusUnsupportedMediaType,
		},
		{
			name:        "the legacy unattested route takes no video",
			contentType: "video/mp4",
			body:        isoBMFFHeader(),
			target:      "/certificates",
			wantStatus:  http.StatusUnsupportedMediaType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			video := &mockVideoVerifier{}
			mux := http.NewServeMux()
			certs := handler.NewCertificateHandler(
				&mockCertifier{executeFn: certifyOK},
				&mockVerifier{executeFn: verifyNotFound},
				video,
				&mockDeleter{},
				nil,
				true,
			)
			certs.RegisterRoutes(mux, nil, nil)

			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, newUploadRequest(t, http.MethodPost, tt.target, tt.contentType, tt.body))

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body %s)", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if got := video.calls > 0; got != tt.wantVideo {
				t.Errorf("video pipeline used = %v, want %v", got, tt.wantVideo)
			}
		})
	}
}

func TestParseMediaUpload_MissingFileField(t *testing.T) {
	mux := http.NewServeMux()
	certs := handler.NewCertificateHandler(
		&mockCertifier{executeFn: certifyOK},
		&mockVerifier{executeFn: verifyNotFound},
		&mockVideoVerifier{},
		&mockDeleter{},
		nil,
		true,
	)
	certs.RegisterRoutes(mux, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/certificates/verify", nil)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=nope")

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

// Verification stays free, but an unauthenticated caller gets a smaller video
// than an authenticated one: a 256 MB decode per anonymous request is a denial
// of service handed to the internet.
func TestParseMediaUpload_AnonymousVideoIsSizeTiered(t *testing.T) {
	const anonymousLimit = 32 << 20

	// Deliberately chunky. The ceilings are compile-time constants, so the only
	// honest way to exercise the per-kind size check is to exceed one.
	body := append(isoBMFFHeader(), make([]byte, anonymousLimit)...)

	video := &mockVideoVerifier{}
	mux := http.NewServeMux()
	certs := handler.NewCertificateHandler(
		&mockCertifier{executeFn: certifyOK},
		&mockVerifier{executeFn: verifyNotFound},
		video,
		&mockDeleter{},
		nil,
		true,
	)
	certs.RegisterRoutes(mux, nil, nil)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, newUploadRequest(t, http.MethodPost, "/certificates/verify", "video/mp4", body))

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 for an anonymous oversized video (body %s)", rr.Code, rr.Body.String())
	}
	if video.calls != 0 {
		t.Error("the video pipeline ran for an upload that should have been refused on size")
	}
}

// An empty part cannot be a container, and the check has to say so rather than
// hand zero bytes to the decoder.
func TestParseMediaUpload_EmptyVideoPart(t *testing.T) {
	mux := http.NewServeMux()
	certs := handler.NewCertificateHandler(
		&mockCertifier{executeFn: certifyOK},
		&mockVerifier{executeFn: verifyNotFound},
		&mockVideoVerifier{},
		&mockDeleter{},
		nil,
		true,
	)
	certs.RegisterRoutes(mux, nil, nil)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, newUploadRequest(t, http.MethodPost, "/certificates/verify", "video/mp4", nil))

	if rr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415 (body %s)", rr.Code, rr.Body.String())
	}
}

// An authenticated caller gets the full video ceiling, where an anonymous one
// is held to a smaller share.
func TestParseMediaUpload_AuthenticatedVideoGetsTheFullCeiling(t *testing.T) {
	const anonymousLimit = 32 << 20
	body := append(isoBMFFHeader(), make([]byte, anonymousLimit)...)

	video := &mockVideoVerifier{}
	quota := &mockQuota{}
	mux := http.NewServeMux()
	certs := handler.NewCertificateHandler(
		&mockCertifier{executeFn: certifyOK},
		&mockVerifier{executeFn: verifyNotFound},
		video,
		&mockDeleter{},
		quota,
		true,
	)
	certs.RegisterRoutes(mux, nil, nil)
	guarded := handler.OptionalAPIKeyAuth(okAuthenticator())(mux)

	req := newUploadRequest(t, http.MethodPost, "/certificates/verify", "video/mp4", body)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)

	rr := httptest.NewRecorder()
	guarded.ServeHTTP(rr, req)

	// Not certified, so 404 — but it got past the size gate, which is the point.
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rr.Code, rr.Body.String())
	}
	if video.calls != 1 {
		t.Errorf("video pipeline ran %d time(s), want 1", video.calls)
	}
}

// A quota write that fails must not fail a verification that already succeeded.
func TestVerifyByFile_UsageWriteFailureIsTolerated(t *testing.T) {
	quota := &mockQuota{recordFn: func(context.Context, string, domain.Operation) error {
		return errors.New("counter unavailable")
	}}

	mux := http.NewServeMux()
	certs := handler.NewCertificateHandler(
		&mockCertifier{executeFn: certifyOK},
		&mockVerifier{executeFn: verifyFound},
		&mockVideoVerifier{},
		&mockDeleter{},
		quota,
		true,
	)
	certs.RegisterRoutes(mux, nil, nil)
	guarded := handler.OptionalAPIKeyAuth(okAuthenticator())(mux)

	req := newUploadRequest(t, http.MethodPost, "/certificates/verify", "image/jpeg", []byte("img"))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)

	rr := httptest.NewRecorder()
	guarded.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (body %s)", rr.Code, rr.Body.String())
	}
}

// The capture route rejects a media type it does not accept before it reaches
// the use case at all.
func TestCapture_RejectsAnUnsupportedMediaType(t *testing.T) {
	called := false
	capturer := &mockCapturer{fn: func(context.Context, usecase.AttestedCaptureInput) (*usecase.CertifyOutput, error) {
		called = true
		return nil, nil
	}}
	mux := newCaptureMux(t, capturer, nil, nil, nil)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, captureRequestWithMedia(
		t, validCaptureFields(), "capture.pdf", "application/pdf", []byte("%PDF-1.4")))

	if rr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415 (body %s)", rr.Code, rr.Body.String())
	}
	if called {
		t.Error("the use case ran for a media type the upload layer should have refused")
	}
}

func TestCapture_DeviceKeyInUse(t *testing.T) {
	capturer := &mockCapturer{fn: func(context.Context, usecase.AttestedCaptureInput) (*usecase.CertifyOutput, error) {
		return nil, domain.ErrDeviceKeyInUse
	}}
	mux := newCaptureMux(t, capturer, nil, nil, nil)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, captureRequest(t, validCaptureFields()))

	if rr.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (body %s)", rr.Code, rr.Body.String())
	}
}
