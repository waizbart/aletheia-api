package handler

import (
	"context"
	"net/http"
	"strings"
)

type ctxKey int

const identityKey ctxKey = iota

// apiKeyHeader is the header clients send their API key in.
const apiKeyHeader = "X-API-Key"

// IdentityFromContext returns the authenticated registrant identity injected by
// APIKeyAuth, or "" when the request was not authenticated (exempt routes).
func IdentityFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(identityKey).(string); ok {
		return v
	}
	return ""
}

// withIdentity returns a copy of ctx carrying the authenticated identity.
func withIdentity(ctx context.Context, identity string) context.Context {
	return context.WithValue(ctx, identityKey, identity)
}

// authExemptPrefixes are paths reachable without an API key: health probe, API
// docs, and the observability dashboard.
var authExemptPrefixes = []string{"/health", "/docs", "/observability"}

func isAuthExempt(path string) bool {
	for _, p := range authExemptPrefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// APIKeyAuth gates every non-exempt route on a valid X-API-Key header. keys maps
// an API key to the registrant identity it authenticates as; that identity is
// injected into the request context (see IdentityFromContext) so handlers derive
// the registrant from the authenticated principal instead of a spoofable header.
func APIKeyAuth(keys map[string]string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isAuthExempt(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			key := r.Header.Get(apiKeyHeader)
			identity, ok := keys[key]
			if key == "" || !ok {
				writeError(w, http.StatusUnauthorized, "missing or invalid API key")
				return
			}

			next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), identity)))
		})
	}
}
