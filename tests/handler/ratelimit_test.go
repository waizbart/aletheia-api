package handler_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/waizbart/aletheia-api/internal/handler"
)

// fakeClock drives the limiter without sleeping.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestRateLimiter_BurstThenThrottle(t *testing.T) {
	clock := newFakeClock()
	l := handler.NewRateLimiter(1, 3, handler.WithClock(clock.Now))

	for i := 0; i < 3; i++ {
		if !l.Allow("client-a") {
			t.Fatalf("request %d within burst should be allowed", i+1)
		}
	}
	if l.Allow("client-a") {
		t.Fatal("request beyond burst should be denied")
	}
}

func TestRateLimiter_RefillsOverTime(t *testing.T) {
	clock := newFakeClock()
	l := handler.NewRateLimiter(2, 2, handler.WithClock(clock.Now))

	l.Allow("c")
	l.Allow("c")
	if l.Allow("c") {
		t.Fatal("bucket should be empty")
	}

	// At 2 tokens/second, half a second buys exactly one token back.
	clock.Advance(500 * time.Millisecond)
	if !l.Allow("c") {
		t.Fatal("expected one refilled token")
	}
	if l.Allow("c") {
		t.Fatal("only one token should have refilled")
	}
}

func TestRateLimiter_RefillIsCappedAtBurst(t *testing.T) {
	clock := newFakeClock()
	l := handler.NewRateLimiter(10, 2, handler.WithClock(clock.Now))

	l.Allow("c")
	clock.Advance(time.Hour)

	if !l.Allow("c") || !l.Allow("c") {
		t.Fatal("burst capacity should be restored")
	}
	if l.Allow("c") {
		t.Fatal("refill must not exceed burst")
	}
}

func TestRateLimiter_BucketsAreIndependentPerClient(t *testing.T) {
	clock := newFakeClock()
	l := handler.NewRateLimiter(1, 1, handler.WithClock(clock.Now))

	if !l.Allow("client-a") {
		t.Fatal("client-a first request should pass")
	}
	if !l.Allow("client-b") {
		t.Fatal("client-b must not inherit client-a's consumption")
	}
	if l.Allow("client-a") {
		t.Fatal("client-a should now be throttled")
	}
}

func TestRateLimiter_DisabledWhenRateNonPositive(t *testing.T) {
	l := handler.NewRateLimiter(0, 0)
	for i := 0; i < 100; i++ {
		if !l.Allow("anyone") {
			t.Fatal("a non-positive rate disables limiting")
		}
	}
	if l.Size() != 0 {
		t.Error("disabled limiter should not allocate buckets")
	}
}

func TestRateLimiter_SweepsIdleBuckets(t *testing.T) {
	clock := newFakeClock()
	l := handler.NewRateLimiter(1, 1,
		handler.WithClock(clock.Now),
		handler.WithIdleTTL(time.Minute),
	)

	l.Allow("stale")
	if l.Size() != 1 {
		t.Fatalf("Size = %d, want 1", l.Size())
	}

	// Past the TTL, the next call sweeps the untouched bucket and creates its
	// own — so one entry remains, but it belongs to the new client.
	clock.Advance(2 * time.Minute)
	l.Allow("fresh")

	if got := l.Size(); got != 1 {
		t.Fatalf("Size = %d, want 1 after sweep", got)
	}
	// "stale" was evicted, so it gets a full burst again rather than a
	// carried-over empty bucket.
	if !l.Allow("stale") {
		t.Error("evicted bucket should be recreated with full burst")
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	clock := newFakeClock()
	l := handler.NewRateLimiter(1, 1, handler.WithClock(clock.Now))

	var calls int
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	})
	wrapped := handler.RateLimit(l, 0)(inner)

	req := func() *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/health", nil)
		r.RemoteAddr = "203.0.113.9:5555"
		wrapped.ServeHTTP(rr, r)
		return rr
	}

	if got := req().Code; got != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", got)
	}

	rr := req()
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After on 429")
	}
	if calls != 1 {
		t.Errorf("inner handler called %d times, want 1", calls)
	}
}

func TestConcurrencyLimit_RejectsOverCap(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	})
	wrapped := handler.ConcurrencyLimit(1)(inner)

	go func() {
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/certificates", nil))
	}()

	<-entered // the single slot is now held

	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/certificates", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After on 503")
	}

	close(release)
}

func TestConcurrencyLimit_SlotIsReleased(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrapped := handler.ConcurrencyLimit(1)(inner)

	for i := 0; i < 5; i++ {
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("sequential request %d status = %d, want 200", i, rr.Code)
		}
	}
}

func TestConcurrencyLimit_DisabledWhenNonPositive(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	wrapped := handler.ConcurrencyLimit(0)(inner)

	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rr.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want pass-through 418", rr.Code)
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		hops       int
		want       string
	}{
		{
			name:       "remote addr host when no proxy is configured",
			remoteAddr: "198.51.100.7:41234",
			want:       "198.51.100.7",
		},
		{
			// With no proxy in front, honouring XFF would let any caller forge
			// a fresh identity per request and evade the limiter entirely.
			name:       "forwarded header ignored when no proxy is configured",
			remoteAddr: "198.51.100.7:41234",
			headers:    map[string]string{"X-Forwarded-For": "1.2.3.4"},
			want:       "198.51.100.7",
		},
		{
			name:       "single trusted proxy appended the only entry",
			remoteAddr: "10.0.0.1:8080",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.9"},
			hops:       1,
			want:       "203.0.113.9",
		},
		{
			// The whole point of counting from the right: the caller put
			// 1.2.3.4 in front, the proxy appended the address it actually saw.
			// Taking the leftmost entry would hand the caller a new bucket per
			// request.
			name:       "spoofed leftmost entry is ignored",
			remoteAddr: "10.0.0.1:8080",
			headers:    map[string]string{"X-Forwarded-For": "1.2.3.4, 203.0.113.9"},
			hops:       1,
			want:       "203.0.113.9",
		},
		{
			name:       "two trusted hops count back two entries",
			remoteAddr: "10.0.0.1:8080",
			headers:    map[string]string{"X-Forwarded-For": "1.2.3.4, 203.0.113.9, 10.0.0.2"},
			hops:       2,
			want:       "203.0.113.9",
		},
		{
			// Fewer entries than configured hops means the request did not come
			// through the expected proxies, so nothing in the header is
			// trustworthy.
			name:       "chain shorter than the hop count falls back to remote addr",
			remoteAddr: "10.0.0.1:8080",
			headers:    map[string]string{"X-Forwarded-For": "203.0.113.9"},
			hops:       3,
			want:       "10.0.0.1",
		},
		{
			name:       "empty forwarded header falls back to remote addr",
			remoteAddr: "10.0.0.1:8080",
			headers:    map[string]string{"X-Forwarded-For": ""},
			hops:       1,
			want:       "10.0.0.1",
		},
		{
			// A non-address would otherwise become its own rate-limit bucket,
			// which is a free bypass for anything the proxy passes through.
			name:       "non-ip entry falls back to remote addr",
			remoteAddr: "10.0.0.1:8080",
			headers:    map[string]string{"X-Forwarded-For": "not-an-ip"},
			hops:       1,
			want:       "10.0.0.1",
		},
		{
			name:       "blank entry falls back to remote addr",
			remoteAddr: "10.0.0.1:8080",
			headers:    map[string]string{"X-Forwarded-For": "1.2.3.4,   "},
			hops:       1,
			want:       "10.0.0.1",
		},
		{
			name:       "surrounding whitespace is trimmed",
			remoteAddr: "10.0.0.1:8080",
			headers:    map[string]string{"X-Forwarded-For": "  203.0.113.9  "},
			hops:       1,
			want:       "203.0.113.9",
		},
		{
			name:       "ipv6 entry is accepted",
			remoteAddr: "10.0.0.1:8080",
			headers:    map[string]string{"X-Forwarded-For": "2001:db8::1"},
			hops:       1,
			want:       "2001:db8::1",
		},
		{
			// X-Real-Ip carries no chain, so a proxy that forwards rather than
			// overwrites it would hand the caller a free spoof. It is never
			// consulted.
			name:       "real-ip header is never trusted",
			remoteAddr: "10.0.0.1:8080",
			headers:    map[string]string{"X-Real-Ip": "5.6.7.8"},
			hops:       1,
			want:       "10.0.0.1",
		},
		{
			name:       "negative hop count behaves as no proxy",
			remoteAddr: "198.51.100.7:41234",
			headers:    map[string]string{"X-Forwarded-For": "1.2.3.4"},
			hops:       -1,
			want:       "198.51.100.7",
		},
		{
			name:       "unparseable remote addr is used verbatim",
			remoteAddr: "not-a-host-port",
			want:       "not-a-host-port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remoteAddr
			for k, v := range tt.headers {
				r.Header.Set(k, v)
			}

			if got := handler.ClientIP(r, tt.hops); got != tt.want {
				t.Errorf("ClientIP = %q, want %q", got, tt.want)
			}
		})
	}
}

// A client that forges a new X-Forwarded-For per request must not get a new
// rate-limit bucket per request. This is the bypass the hop counting exists to
// close, so it is asserted end to end through the middleware.
func TestRateLimit_SpoofedForwardedHeaderCannotEvadeTheLimiter(t *testing.T) {
	l := handler.NewRateLimiter(1, 1)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrapped := handler.RateLimit(l, 1)(inner)

	var lastCode int
	for i := 0; i < 5; i++ {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = "10.0.0.1:8080"
		// A different forged value each time, with the trusted proxy's entry
		// appended after it.
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("1.2.3.%d, 203.0.113.9", i))

		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, r)
		lastCode = rr.Code
	}

	if lastCode != http.StatusTooManyRequests {
		t.Errorf("last status = %d, want %d — the forged header bought a fresh bucket",
			lastCode, http.StatusTooManyRequests)
	}
	if n := l.Size(); n != 1 {
		t.Errorf("bucket count = %d, want 1 — forged headers are growing the map", n)
	}
}
