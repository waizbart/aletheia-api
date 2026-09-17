package handler_test

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/waizbart/aletheia-api/internal/usecase"
)

type mockCertifier struct {
	executeFn func(ctx context.Context, in usecase.CertifyInput) (*usecase.CertifyOutput, error)
}

func (m *mockCertifier) Execute(ctx context.Context, in usecase.CertifyInput) (*usecase.CertifyOutput, error) {
	return m.executeFn(ctx, in)
}

type mockVerifier struct {
	executeFn func(ctx context.Context, in usecase.VerifyInput) (*usecase.VerifyOutput, error)
}

func (m *mockVerifier) Execute(ctx context.Context, in usecase.VerifyInput) (*usecase.VerifyOutput, error) {
	return m.executeFn(ctx, in)
}

type mockDeleter struct {
	executeFn func(ctx context.Context, in usecase.DeleteInput) error
}

func (m *mockDeleter) Execute(ctx context.Context, in usecase.DeleteInput) error {
	if m.executeFn == nil {
		return nil
	}
	return m.executeFn(ctx, in)
}

func newUploadRequest(t *testing.T, method, target, contentType string, body []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="test.dat"`))
	h.Set("Content-Type", contentType)

	pw, err := mw.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	pw.Write(body)
	mw.Close()

	req := httptest.NewRequest(method, target, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

type mockVideoVerifier struct {
	executeFn func(ctx context.Context, in usecase.VerifyVideoInput) (*usecase.VerifyOutput, error)
	calls     int
}

func (m *mockVideoVerifier) Execute(ctx context.Context, in usecase.VerifyVideoInput) (*usecase.VerifyOutput, error) {
	m.calls++
	if m.executeFn == nil {
		return &usecase.VerifyOutput{Certified: false}, nil
	}
	return m.executeFn(ctx, in)
}

// isoBMFFHeader is the smallest thing looksLikeVideo accepts by matching the
// ftyp box directly: a box length, "ftyp", and a brand. Go's own sniff table
// maps only a few brands, which is exactly why the direct check exists.
func isoBMFFHeader() []byte {
	head := []byte{
		0x00, 0x00, 0x00, 0x18,
		'f', 't', 'y', 'p',
		'i', 's', 'o', 'm',
		0x00, 0x00, 0x02, 0x00,
	}
	// Pad past the sniff window so DetectContentType sees a full buffer.
	return append(head, make([]byte, 600)...)
}

// webmHeader is a container Go's sniff table does recognise, so it exercises
// the other half of looksLikeVideo.
func webmHeader() []byte {
	return append([]byte{0x1a, 0x45, 0xdf, 0xa3}, make([]byte, 600)...)
}
