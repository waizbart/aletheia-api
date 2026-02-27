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
