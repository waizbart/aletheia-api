package handler

import (
	"log"
	"net/http"
	"strings"
	"time"
)

func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

// CORS returns a middleware that answers cross-origin browser requests and the
// preflight OPTIONS that precedes them. Swagger UI's "Try it out" triggers a
// preflight because the X-Registrant header is non-simple; without these
// headers the browser aborts with a generic network error.
//
// allowedOrigins lists the permitted origins. A single "*" entry (or an empty
// list) permits any origin. Because the API uses no cookies or other
// credentials, a wildcard origin is safe; lock it down via CORS_ALLOWED_ORIGINS
// when fronting the API with credentialed clients.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := normalizeOrigins(allowedOrigins)
	allowAll := len(allowed) == 0 || allowed["*"]

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if origin := r.Header.Get("Origin"); origin != "" {
				switch {
				case allowAll:
					w.Header().Set("Access-Control-Allow-Origin", "*")
				case allowed[origin]:
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Add("Vary", "Origin")
				}
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", corsAllowHeaders(r))
				w.Header().Set("Access-Control-Max-Age", "600")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// corsAllowHeaders echoes the headers the browser asked for in its preflight,
// falling back to the set this API actually reads.
func corsAllowHeaders(r *http.Request) string {
	if req := r.Header.Get("Access-Control-Request-Headers"); req != "" {
		return req
	}
	return "Content-Type, Accept, X-Registrant"
}

func normalizeOrigins(origins []string) map[string]bool {
	set := make(map[string]bool, len(origins))
	for _, o := range origins {
		if o = strings.TrimSpace(o); o != "" {
			set[o] = true
		}
	}
	return set
}
