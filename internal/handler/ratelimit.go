package handler

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiter applies a token-bucket limit per caller. The caller is keyed by
// authenticated identity when present (so one client's burst doesn't starve
// another), falling back to remote IP for exempt/unauthenticated routes.
type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rps      rate.Limit
	burst    int
	now      func() time.Time // injectable for tests
}

func NewRateLimiter(rps float64, burst int) *RateLimiter {
	if burst <= 0 {
		burst = 1
	}
	return &RateLimiter{
		visitors: make(map[string]*visitor),
		rps:      rate.Limit(rps),
		burst:    burst,
		now:      time.Now,
	}
}

func (rl *RateLimiter) limiterFor(key string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	// Opportunistic prune of stale visitors to bound memory.
	if len(rl.visitors) > 1024 {
		for k, v := range rl.visitors {
			if now.Sub(v.lastSeen) > 10*time.Minute {
				delete(rl.visitors, k)
			}
		}
	}

	v, ok := rl.visitors[key]
	if !ok {
		v = &visitor{limiter: rate.NewLimiter(rl.rps, rl.burst)}
		rl.visitors[key] = v
	}
	v.lastSeen = now
	return v.limiter
}

// Middleware enforces the rate limit. Exempt routes (health, docs, dashboard)
// are not limited so monitoring and the live UI keep working under load.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAuthExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// Key by API key when supplied (per-client limit) so one caller's burst
		// doesn't starve another; fall back to remote IP. Runs before auth so a
		// flood of invalid keys is still limited, by IP.
		key := r.Header.Get(apiKeyHeader)
		if key == "" {
			key = clientIP(r)
		}

		if !rl.limiterFor(key).Allow() {
			w.Header().Set("Retry-After", strconv.Itoa(1))
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
