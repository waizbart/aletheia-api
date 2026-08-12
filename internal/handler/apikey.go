package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/waizbart/aletheia-api/internal/domain"
)

// Authenticator resolves a presented credential to its owning organisation.
type Authenticator interface {
	Execute(ctx context.Context, plaintext string) (*domain.Org, error)
}

type orgContextKey struct{}

// WithOrg attaches an authenticated organisation to the request context.
func WithOrg(ctx context.Context, org *domain.Org) context.Context {
	return context.WithValue(ctx, orgContextKey{}, org)
}

// OrgFromContext returns the authenticated organisation, if any. Handlers
// mounted behind APIKeyAuth can rely on it being present.
func OrgFromContext(ctx context.Context) *domain.Org {
	org, _ := ctx.Value(orgContextKey{}).(*domain.Org)
	return org
}

// APIKeyAuth authenticates tenant requests from an Authorization bearer token
// and puts the resolved organisation on the request context.
//
// Every rejection reason collapses to the same 401: distinguishing "unknown
// key" from "revoked key" would let an attacker confirm which credentials once
// existed.
func APIKeyAuth(auth Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				unauthorized(w)
				return
			}

			org, err := auth.Execute(r.Context(), token)
			if err != nil {
				if errors.Is(err, domain.ErrUnauthorized) {
					unauthorized(w)
					return
				}
				// A storage failure is ours, not the caller's.
				writeError(w, http.StatusInternalServerError, "could not verify credentials")
				return
			}

			next.ServeHTTP(w, r.WithContext(WithOrg(r.Context(), org)))
		})
	}
}

// OptionalAPIKeyAuth attaches an organisation when the caller presents a valid
// credential, and lets everyone else through anonymously.
//
// Public verification is free and must stay reachable without an account — it
// is what makes certificates worth buying. Authenticated verification is
// metered, so the same route serves both and the presence of an org on the
// context decides which.
func OptionalAPIKeyAuth(auth Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			org, err := auth.Execute(r.Context(), token)
			if err != nil || org == nil {
				// A bad credential on an anonymous-capable route is treated as
				// no credential rather than as an error: the caller still gets
				// the free tier.
				next.ServeHTTP(w, r)
				return
			}

			next.ServeHTTP(w, r.WithContext(WithOrg(r.Context(), org)))
		})
	}
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="aletheia"`)
	writeError(w, http.StatusUnauthorized, "a valid API key is required")
}

// QuotaChecker reports whether an org may perform another billable operation,
// and counts the ones that succeed.
type QuotaChecker interface {
	Check(ctx context.Context, org *domain.Org, op domain.Operation) error
	Record(ctx context.Context, orgID string, op domain.Operation) error
}
