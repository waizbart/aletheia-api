package handler

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RateLimiter is a per-client token bucket. Buckets refill continuously at
// ratePerSecond and are capped at burst, so a caller may spike up to burst and
// then settles into the sustained rate.
//
// Idle buckets are swept on write so a long-running process does not accumulate
// one bucket per IP that ever touched the API.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket

	rate  float64
	burst float64
	ttl   time.Duration

	// now is injectable so tests can advance time without sleeping.
	now func() time.Time

	lastSweep time.Time
}

type bucket struct {
	tokens float64
	seen   time.Time
}

// RateLimiterOption customises a limiter at construction.
type RateLimiterOption func(*RateLimiter)

// WithClock replaces the limiter's time source, so callers can drive refill
// from a simulated clock instead of wall time.
func WithClock(now func() time.Time) RateLimiterOption {
	return func(l *RateLimiter) { l.now = now }
}

// WithIdleTTL sets how long an untouched bucket is retained before being swept.
func WithIdleTTL(ttl time.Duration) RateLimiterOption {
	return func(l *RateLimiter) { l.ttl = ttl }
}

// NewRateLimiter builds a limiter allowing ratePerSecond sustained requests per
// client with a burst allowance. A non-positive rate disables limiting.
func NewRateLimiter(ratePerSecond, burst int, opts ...RateLimiterOption) *RateLimiter {
	l := &RateLimiter{
		buckets: make(map[string]*bucket),
		rate:    float64(ratePerSecond),
		burst:   float64(burst),
		ttl:     10 * time.Minute,
		now:     time.Now,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Size reports how many buckets the limiter is currently tracking. Exposed for
// tests and for operational metrics on memory growth.
func (l *RateLimiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// Allow reports whether the client identified by key may proceed, consuming a
// token when it may.
func (l *RateLimiter) Allow(key string) bool {
	if l.rate <= 0 {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweep(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, seen: now}
		l.buckets[key] = b
	} else {
		elapsed := now.Sub(b.seen).Seconds()
		if elapsed > 0 {
			b.tokens = min(l.burst, b.tokens+elapsed*l.rate)
		}
		b.seen = now
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep drops buckets untouched for longer than the TTL. Called under lock, at
// most once per TTL window so the cost stays amortised.
func (l *RateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < l.ttl {
		return
	}
	l.lastSweep = now
	for k, b := range l.buckets {
		if now.Sub(b.seen) > l.ttl {
			delete(l.buckets, k)
		}
	}
}

// RateLimit rejects requests from clients over their budget with 429 and a
// Retry-After hint.
func RateLimit(l *RateLimiter, trustProxyHeaders bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !l.Allow(ClientIP(r, trustProxyHeaders)) {
				w.Header().Set("Retry-After", "1")
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ConcurrencyLimit bounds how many requests may be in flight at once. Upload
// routes decode whole images into memory, so without this cap a handful of
// concurrent 100 MB uploads is enough to exhaust the process.
//
// Requests over the cap fail fast with 503 rather than queueing, because a
// client waiting behind a full buffer is a client whose request will time out
// anyway.
func ConcurrencyLimit(max int) func(http.Handler) http.Handler {
	if max <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	slots := make(chan struct{}, max)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
				next.ServeHTTP(w, r)
			default:
				w.Header().Set("Retry-After", strconv.Itoa(1))
				writeError(w, http.StatusServiceUnavailable, "server busy, retry shortly")
			}
		})
	}
}

// ClientIP resolves the caller's address. X-Forwarded-For is honoured only when
// the deployment sits behind a trusted proxy — otherwise any client could spoof
// the header and sidestep rate limiting entirely.
func ClientIP(r *http.Request, trustProxyHeaders bool) string {
	if trustProxyHeaders {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			if first = strings.TrimSpace(first); first != "" {
				return first
			}
		}
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-Ip")); realIP != "" {
			return realIP
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
