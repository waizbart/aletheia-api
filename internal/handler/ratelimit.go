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
func RateLimit(l *RateLimiter, trustedProxyHops int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !l.Allow(ClientIP(r, trustedProxyHops)) {
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

// ClientIP resolves the caller's address, counting back trustedProxyHops
// entries from the right of X-Forwarded-For.
//
// Reading the leftmost entry — the obvious implementation — is wrong even
// behind a proxy you control, because proxies *append*: a client that sends
// its own X-Forwarded-For puts an attacker-chosen value in front of the real
// one. A fresh value per request then means a fresh rate-limit bucket per
// request, which both defeats the limiter and grows the bucket map until the
// idle sweep runs.
//
// Only the entries a trusted proxy appended are trustworthy, so the client is
// the one trustedProxyHops from the end. With hops <= 0 the header is ignored
// entirely and the transport address wins.
//
// X-Real-Ip is deliberately not consulted: it carries no chain, so there is no
// way to tell a value a proxy set from one a client sent, and a proxy that
// forwards rather than overwrites it hands the caller a free spoof.
func ClientIP(r *http.Request, trustedProxyHops int) string {
	if trustedProxyHops > 0 {
		if ip, ok := forwardedFor(r.Header.Get("X-Forwarded-For"), trustedProxyHops); ok {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// forwardedFor picks the entry hops from the right of an X-Forwarded-For
// chain, reporting false when the chain is too short to contain it or the
// value is not an IP address.
//
// A chain shorter than the configured hop count means the request did not
// arrive through the expected proxies, so nothing in it can be trusted; the
// parse check stops a garbage value from becoming its own rate-limit bucket.
func forwardedFor(header string, hops int) (string, bool) {
	if header == "" {
		return "", false
	}
	parts := strings.Split(header, ",")
	idx := len(parts) - hops
	if idx < 0 {
		return "", false
	}
	candidate := strings.TrimSpace(parts[idx])
	if net.ParseIP(candidate) == nil {
		return "", false
	}
	return candidate, true
}
