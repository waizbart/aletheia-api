package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/handler"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

func videoVerifyMux(t *testing.T, video handler.VideoVerifier) *http.ServeMux {
	t.Helper()
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
	return mux
}

func postVideo(t *testing.T, mux *http.ServeMux) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, newUploadRequest(t, http.MethodPost, "/certificates/verify", "video/mp4", isoBMFFHeader()))
	return rr
}

// A deployment that did not wire video verification must say so, rather than
// panic on a nil port.
func TestVerifyByFile_VideoUnconfigured(t *testing.T) {
	rr := postVideo(t, videoVerifyMux(t, nil))
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415 (body %s)", rr.Code, rr.Body.String())
	}
}

// Each rejection maps to the status a caller can act on: retry, shorten the
// clip, or re-encode it. A blanket 500 would tell them none of that.
func TestVerifyByFile_VideoErrorStatuses(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"too large", domain.ErrVideoTooLarge, http.StatusRequestEntityTooLarge},
		{"too long", domain.ErrVideoTooLong, http.StatusUnprocessableEntity},
		{"resolution", domain.ErrVideoResolution, http.StatusUnprocessableEntity},
		{"undecodable", domain.ErrVideoUndecodable, http.StatusUnsupportedMediaType},
		{"anything else", errors.New("boom"), http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			video := &mockVideoVerifier{
				executeFn: func(context.Context, usecase.VerifyVideoInput) (*usecase.VerifyOutput, error) {
					return nil, tt.err
				},
			}
			rr := postVideo(t, videoVerifyMux(t, video))
			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body %s)", rr.Code, tt.wantStatus, rr.Body.String())
			}
		})
	}
}

// The response has to carry everything a third party needs to recompute the
// commitment, or the certificate cannot be checked without trusting this API.
func TestVerifyByFile_VideoResponseCarriesCommitmentInputs(t *testing.T) {
	frames := make([][32]byte, 12)
	for i := range frames {
		frames[i][0] = byte(i)
	}
	cert := &domain.Certificate{
		ID:          "cert-video",
		ContentHash: "hash",
		MediaKind:   domain.MediaKindVideo,
		DurationMs:  8_000,
		CreatedAt:   time.Now(),
		Video: &domain.VideoSignature{
			FramePHashes: frames,
			AnchorIndex:  7,
			DurationMs:   8_000,
		},
	}

	video := &mockVideoVerifier{
		executeFn: func(context.Context, usecase.VerifyVideoInput) (*usecase.VerifyOutput, error) {
			return &usecase.VerifyOutput{Certified: true, Certificate: cert}, nil
		},
	}

	rr := postVideo(t, videoVerifyMux(t, video))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rr.Code, rr.Body.String())
	}

	var body struct {
		Certified   bool `json:"certified"`
		Certificate struct {
			MediaKind   string `json:"media_kind"`
			DurationMs  int    `json:"duration_ms"`
			FrameCount  int    `json:"frame_count"`
			AnchorIndex *int   `json:"anchor_index"`
		} `json:"certificate"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if !body.Certified {
		t.Error("certified = false, want true")
	}
	if body.Certificate.MediaKind != string(domain.MediaKindVideo) {
		t.Errorf("media_kind = %q, want video", body.Certificate.MediaKind)
	}
	if body.Certificate.DurationMs != 8_000 {
		t.Errorf("duration_ms = %d, want 8000", body.Certificate.DurationMs)
	}
	if body.Certificate.FrameCount != 12 {
		t.Errorf("frame_count = %d, want 12", body.Certificate.FrameCount)
	}
	// Zero is a legitimate anchor index, so the field is a pointer and must be
	// present rather than omitted.
	if body.Certificate.AnchorIndex == nil || *body.Certificate.AnchorIndex != 7 {
		t.Errorf("anchor_index = %v, want 7", body.Certificate.AnchorIndex)
	}
}

// An image certificate must keep reporting media_kind, including on rows that
// predate the column.
func TestVerifyByFile_ImageResponseReportsMediaKind(t *testing.T) {
	legacy := &domain.Certificate{ID: "legacy", ContentHash: "hash", CreatedAt: time.Now()}

	mux := http.NewServeMux()
	certs := handler.NewCertificateHandler(
		&mockCertifier{executeFn: certifyOK},
		&mockVerifier{executeFn: func(context.Context, usecase.VerifyInput) (*usecase.VerifyOutput, error) {
			return &usecase.VerifyOutput{Certified: true, Certificate: legacy}, nil
		}},
		&mockVideoVerifier{},
		&mockDeleter{},
		nil,
		true,
	)
	certs.RegisterRoutes(mux, nil, nil)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, newUploadRequest(t, http.MethodPost, "/certificates/verify", "image/jpeg", []byte("img")))

	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	certificate, _ := body["certificate"].(map[string]any)
	if got := certificate["media_kind"]; got != string(domain.MediaKindImage) {
		t.Errorf("media_kind = %v, want image", got)
	}
	if _, present := certificate["anchor_index"]; present {
		t.Error("an image certificate must not report an anchor index")
	}
}

// --- attested video capture -------------------------------------------------

func TestCapture_VideoIsMeteredSeparately(t *testing.T) {
	var forwarded domain.MediaKind
	capturer := &mockCapturer{fn: func(_ context.Context, in usecase.AttestedCaptureInput) (*usecase.CertifyOutput, error) {
		forwarded = in.MediaKind
		return &usecase.CertifyOutput{Certificate: &domain.Certificate{
			ID: "cert-video", MediaKind: domain.MediaKindVideo, CreatedAt: time.Now(),
		}}, nil
	}}
	quota := &mockQuota{}
	mux := newCaptureMux(t, capturer, quota, nil, nil)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, captureRequestWithMedia(
		t, validCaptureFields(), "capture.mp4", "video/mp4", isoBMFFHeader()))

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rr.Code, rr.Body.String())
	}
	if forwarded != domain.MediaKindVideo {
		t.Errorf("forwarded media kind = %q, want video", forwarded)
	}
	// Both the check and the record have to name the video operation, or a
	// video is billed against the image allowance.
	if len(quota.checked) != 1 || quota.checked[0] != domain.OpAttestedVideoCapture {
		t.Errorf("checked operations = %v, want [%s]", quota.checked, domain.OpAttestedVideoCapture)
	}
	if len(quota.recorded) != 1 || quota.recorded[0] != domain.OpAttestedVideoCapture {
		t.Errorf("recorded operations = %v, want [%s]", quota.recorded, domain.OpAttestedVideoCapture)
	}
}

func TestCapture_ImageStillUsesTheImageOperation(t *testing.T) {
	quota := &mockQuota{}
	mux := newCaptureMux(t, nil, quota, nil, nil)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, captureRequest(t, validCaptureFields()))

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rr.Code, rr.Body.String())
	}
	if len(quota.checked) != 1 || quota.checked[0] != domain.OpAttestedCapture {
		t.Errorf("checked operations = %v, want [%s]", quota.checked, domain.OpAttestedCapture)
	}
}

func TestCapture_VideoOverQuota(t *testing.T) {
	quota := &mockQuota{checkErr: domain.ErrQuotaExceeded}
	mux := newCaptureMux(t, nil, quota, nil, nil)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, captureRequestWithMedia(
		t, validCaptureFields(), "capture.mp4", "video/mp4", isoBMFFHeader()))

	if rr.Code != http.StatusPaymentRequired {
		t.Errorf("status = %d, want 402 (body %s)", rr.Code, rr.Body.String())
	}
	if len(quota.recorded) != 0 {
		t.Error("a refused capture was billed")
	}
}

// The use case's video rejections have to reach the client as themselves, not
// flattened into the generic 422 the capture path uses for everything else.
func TestCapture_VideoRejectionStatuses(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"too long", domain.ErrVideoTooLong, http.StatusUnprocessableEntity},
		{"undecodable", domain.ErrVideoUndecodable, http.StatusUnsupportedMediaType},
		{"too large", domain.ErrVideoTooLarge, http.StatusRequestEntityTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capturer := &mockCapturer{fn: func(context.Context, usecase.AttestedCaptureInput) (*usecase.CertifyOutput, error) {
				return nil, tt.err
			}}
			mux := newCaptureMux(t, capturer, nil, nil, nil)

			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, captureRequestWithMedia(
				t, validCaptureFields(), "capture.mp4", "video/mp4", isoBMFFHeader()))

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body %s)", rr.Code, tt.wantStatus, rr.Body.String())
			}
		})
	}
}
