package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/waizbart/aletheia-api/internal/handler"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func doReq(h http.Handler, path, key string) int {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code
}

func TestRateLimiter_BlocksAfterBurst(t *testing.T) {
	// rps 0 means no refill within the test window; burst of 2 allows exactly 2.
	rl := handler.NewRateLimiter(0, 2)
	h := rl.Middleware(okHandler())

	if code := doReq(h, "/certificates", "k1"); code != http.StatusOK {
		t.Fatalf("req 1 = %d, want 200", code)
	}
	if code := doReq(h, "/certificates", "k1"); code != http.StatusOK {
		t.Fatalf("req 2 = %d, want 200", code)
	}
	if code := doReq(h, "/certificates", "k1"); code != http.StatusTooManyRequests {
		t.Fatalf("req 3 = %d, want 429", code)
	}
}

func TestRateLimiter_IsolatesPerKey(t *testing.T) {
	rl := handler.NewRateLimiter(0, 1)
	h := rl.Middleware(okHandler())

	if code := doReq(h, "/certificates", "k1"); code != http.StatusOK {
		t.Fatalf("k1 first = %d, want 200", code)
	}
	if code := doReq(h, "/certificates", "k1"); code != http.StatusTooManyRequests {
		t.Fatalf("k1 second = %d, want 429", code)
	}
	// A different key has its own bucket and is unaffected.
	if code := doReq(h, "/certificates", "k2"); code != http.StatusOK {
		t.Fatalf("k2 first = %d, want 200", code)
	}
}

func TestRateLimiter_ExemptPathsNotLimited(t *testing.T) {
	rl := handler.NewRateLimiter(0, 1)
	h := rl.Middleware(okHandler())

	for i := 0; i < 5; i++ {
		if code := doReq(h, "/health", ""); code != http.StatusOK {
			t.Fatalf("health req %d = %d, want 200 (exempt)", i, code)
		}
	}
}

func TestRateLimiter_KeysByIPWithoutAPIKey(t *testing.T) {
	rl := handler.NewRateLimiter(0, 1)
	h := rl.Middleware(okHandler())

	// No API key: the limiter falls back to the remote IP (same RemoteAddr for
	// both httptest requests), so the second request is throttled.
	if code := doReq(h, "/certificates", ""); code != http.StatusOK {
		t.Fatalf("first = %d, want 200", code)
	}
	if code := doReq(h, "/certificates", ""); code != http.StatusTooManyRequests {
		t.Fatalf("second = %d, want 429", code)
	}
}

func TestRateLimiter_PrunesWhenManyVisitors(t *testing.T) {
	// Exercise the opportunistic prune path: once the visitor map exceeds its
	// bound, subsequent calls run the cleanup scan. Distinct keys keep buckets
	// independent, so every request is allowed.
	rl := handler.NewRateLimiter(1000, 1000)
	h := rl.Middleware(okHandler())

	for i := 0; i < 1100; i++ {
		if code := doReq(h, "/certificates", "key-"+strconv.Itoa(i)); code != http.StatusOK {
			t.Fatalf("request %d = %d, want 200", i, code)
		}
	}
}

func TestRateLimiter_RemoteAddrWithoutPort(t *testing.T) {
	// A RemoteAddr without host:port should still produce a usable key (the raw
	// value) rather than panicking.
	rl := handler.NewRateLimiter(0, 1)
	h := rl.Middleware(okHandler())

	newReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/certificates", nil)
		req.RemoteAddr = "bare-address-no-port"
		return req
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, newReq())
	if rr.Code != http.StatusOK {
		t.Fatalf("first = %d, want 200", rr.Code)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, newReq())
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second = %d, want 429", rr.Code)
	}
}

func TestRateLimiter_BurstClampedToOne(t *testing.T) {
	// burst <= 0 is clamped to 1: exactly one request passes.
	rl := handler.NewRateLimiter(0, 0)
	h := rl.Middleware(okHandler())

	if code := doReq(h, "/certificates", "k1"); code != http.StatusOK {
		t.Fatalf("first = %d, want 200", code)
	}
	if code := doReq(h, "/certificates", "k1"); code != http.StatusTooManyRequests {
		t.Fatalf("second = %d, want 429", code)
	}
}
