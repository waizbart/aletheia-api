// Package attestation verifies that a capture key really lives in a genuine
// device's secure hardware.
//
// This is the trust boundary of the whole product. Everything downstream — the
// certificate, the chain anchor, the provenance claim shown to a verifier —
// inherits its meaning from the check performed here. A capture whose
// attestation does not verify must never become a certificate.
package attestation

import (
	"context"
	"errors"
	"fmt"

	"github.com/waizbart/aletheia-api/internal/domain"
)

// ErrUnsupportedPlatform reports a platform with no verifier registered.
var ErrUnsupportedPlatform = errors.New("attestation: unsupported platform")

// ErrRejected reports an attestation that was structurally understood but
// failed policy: wrong challenge, software-only key, unlocked bootloader,
// unknown app signer. Callers map it to a 403 rather than a 500 — the request
// was well-formed, the device just is not trusted.
type ErrRejected struct{ Reason string }

func (e *ErrRejected) Error() string { return "attestation rejected: " + e.Reason }

func reject(format string, args ...any) error {
	return &ErrRejected{Reason: fmt.Sprintf(format, args...)}
}

// Verifier checks one platform's attestation format. It satisfies the
// usecase.AttestationVerifier port, so the application layer never imports
// this package.
type Verifier interface {
	Verify(ctx context.Context, req domain.AttestationRequest) (*domain.AttestationEvidence, error)
}

// Registry dispatches to the verifier registered for a platform.
type Registry struct {
	verifiers map[domain.Platform]Verifier
}

// NewRegistry builds a dispatcher over the given per-platform verifiers.
func NewRegistry(verifiers map[domain.Platform]Verifier) *Registry {
	m := make(map[domain.Platform]Verifier, len(verifiers))
	for k, v := range verifiers {
		m[k] = v
	}
	return &Registry{verifiers: m}
}

// Verify routes the request to the platform's verifier.
func (r *Registry) Verify(ctx context.Context, req domain.AttestationRequest) (*domain.AttestationEvidence, error) {
	v, ok := r.verifiers[req.Platform]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedPlatform, req.Platform)
	}
	return v.Verify(ctx, req)
}

// Platforms lists the platforms with a registered verifier.
func (r *Registry) Platforms() []domain.Platform {
	out := make([]domain.Platform, 0, len(r.verifiers))
	for p := range r.verifiers {
		out = append(out, p)
	}
	return out
}
