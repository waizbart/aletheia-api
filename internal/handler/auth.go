package handler

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// AdminAuth guards privileged routes (certificate deletion, the observability
// dashboard) with a static bearer token.
//
// It fails closed: an empty token rejects every request rather than disabling
// the guard. An operator who forgets to set ADMIN_API_TOKEN gets a locked door,
// not an open one — the opposite default is how registries get wiped.
func AdminAuth(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !bearerMatches(r, token) {
				w.Header().Set("WWW-Authenticate", `Bearer realm="aletheia-admin"`)
				writeError(w, http.StatusUnauthorized, "admin credentials required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// bearerMatches compares the request's Authorization bearer token against want
// in constant time. An empty want never matches.
func bearerMatches(r *http.Request, want string) bool {
	if want == "" {
		return false
	}
	got, ok := bearerToken(r)
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// bearerToken extracts the credentials from an "Authorization: Bearer <token>"
// header. The scheme match is case-insensitive per RFC 7235.
func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", false
	}
	scheme, credentials, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	credentials = strings.TrimSpace(credentials)
	if credentials == "" {
		return "", false
	}
	return credentials, true
}
