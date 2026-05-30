package handler

import (
	"net/http"

	"github.com/waizbart/aletheia-api/internal/observability"
)

// ObservabilityMiddleware injects a fresh per-request recorder into the context
// for the certify/verify endpoints, so the use cases emit pipeline traces. It
// deliberately skips health, docs and the observability endpoints themselves to
// avoid flooding the trace ring with empty captures.
func ObservabilityMiddleware(f *observability.Factory) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !shouldTrace(r) {
				next.ServeHTTP(w, r)
				return
			}
			name := r.Method + " " + r.URL.Path
			ctx, _, end := f.NewRequest(r.Context(), name)
			defer end()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// shouldTrace matches only the instrumented endpoints: certify (POST
// /certificates) and verify by hash or file (/certificates/verify). DELETE and
// everything else are skipped so the trace ring only holds real pipelines.
func shouldTrace(r *http.Request) bool {
	p := r.URL.Path
	if p == "/certificates" {
		return r.Method == http.MethodPost
	}
	return p == "/certificates/verify"
}
