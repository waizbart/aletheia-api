package handler_test

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waizbart/aletheia-api/internal/handler"
)

func TestLoggingMiddleware_PassesThrough(t *testing.T) {
	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	wrapped := handler.LoggingMiddleware(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	wrapped.ServeHTTP(rr, req)

	if !called {
		t.Fatal("inner handler was not called")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestLoggingMiddleware_Logs(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(nil)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := handler.LoggingMiddleware(inner)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	wrapped.ServeHTTP(rr, req)

	logged := buf.String()
	if !strings.Contains(logged, "GET") || !strings.Contains(logged, "/hello") {
		t.Errorf("log output %q missing method or path", logged)
	}
}
