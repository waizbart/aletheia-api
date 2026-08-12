package usecase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/observability"
)

// IssueNonceUseCase mints the challenge a device must bind its capture to.
type IssueNonceUseCase struct {
	nonces NonceRepository
	ttl    time.Duration
	now    func() time.Time
}

// NewIssueNonceUseCase builds the challenge issuer. A short ttl is deliberate:
// the window between asking for a challenge and uploading the capture is
// seconds, and every extra minute is time an attacker has to work with a
// harvested challenge.
func NewIssueNonceUseCase(nonces NonceRepository, ttl time.Duration, now func() time.Time) *IssueNonceUseCase {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if now == nil {
		now = time.Now
	}
	return &IssueNonceUseCase{nonces: nonces, ttl: ttl, now: now}
}

// Execute issues a single-use challenge for the organisation.
func (uc *IssueNonceUseCase) Execute(ctx context.Context, orgID string) (*domain.CaptureNonce, error) {
	n, err := domain.NewCaptureNonce(orgID, uc.ttl, uc.now())
	if err != nil {
		return nil, fmt.Errorf("issue nonce: %w", err)
	}
	if err := uc.nonces.Save(ctx, n); err != nil {
		return nil, fmt.Errorf("issue nonce: %w", err)
	}
	return &n, nil
}

// EnrollDeviceUseCase turns a verified platform attestation into an enrolled
// device whose public key subsequent captures are checked against.
//
// Attestation is verified once, here, rather than on every capture: the chain
// is large, its verification is expensive, and once the key is pinned a capture
// signature proves the same thing far more cheaply.
type EnrollDeviceUseCase struct {
	devices  DeviceRepository
	nonces   NonceRepository
	verifier AttestationVerifier
	now      func() time.Time
}

func NewEnrollDeviceUseCase(devices DeviceRepository, nonces NonceRepository, verifier AttestationVerifier, now func() time.Time) *EnrollDeviceUseCase {
	if now == nil {
		now = time.Now
	}
	return &EnrollDeviceUseCase{devices: devices, nonces: nonces, verifier: verifier, now: now}
}

type EnrollDeviceInput struct {
	OrgID     string
	Platform  domain.Platform
	Nonce     string
	CertChain [][]byte
	Model     string
}

func (uc *EnrollDeviceUseCase) Execute(ctx context.Context, in EnrollDeviceInput) (*domain.Device, error) {
	if !domain.ValidPlatform(in.Platform) {
		return nil, fmt.Errorf("enroll: unsupported platform %q", in.Platform)
	}
	if len(in.CertChain) == 0 {
		return nil, fmt.Errorf("enroll: attestation certificate chain is required")
	}

	nonce, err := uc.consumeNonce(ctx, in.Nonce, in.OrgID)
	if err != nil {
		return nil, err
	}

	evidence, err := uc.verifier.Verify(ctx, domain.AttestationRequest{
		Platform:  in.Platform,
		Challenge: []byte(nonce.Value),
		CertChain: in.CertChain,
	})
	if err != nil {
		return nil, fmt.Errorf("enroll: %w", err)
	}

	device := &domain.Device{
		OrgID:            in.OrgID,
		Platform:         in.Platform,
		PublicKey:        evidence.PublicKeyDER,
		AttestationLevel: evidence.Level,
		Model:            in.Model,
		Status:           domain.DeviceActive,
		CreatedAt:        uc.now().UTC(),
	}
	if err := uc.devices.Save(ctx, device); err != nil {
		return nil, fmt.Errorf("enroll: saving device: %w", err)
	}
	return device, nil
}

// consumeNonce spends a challenge and confirms it belongs to the caller.
func (uc *EnrollDeviceUseCase) consumeNonce(ctx context.Context, value, orgID string) (*domain.CaptureNonce, error) {
	if !domain.ValidNonceFormat(value) {
		return nil, fmt.Errorf("enroll: %w", domain.ErrNonceUnusable)
	}
	n, err := uc.nonces.Consume(ctx, value, uc.now())
	if err != nil {
		return nil, fmt.Errorf("enroll: %w", err)
	}
	if n.OrgID != orgID {
		// Issued to somebody else. Report it as unusable rather than as a
		// mismatch so the response cannot be used to probe other tenants.
		return nil, fmt.Errorf("enroll: %w", domain.ErrNonceUnusable)
	}
	return n, nil
}

// RevokeDeviceUseCase withdraws a device's ability to make new captures.
type RevokeDeviceUseCase struct {
	devices DeviceRepository
	now     func() time.Time
}

func NewRevokeDeviceUseCase(devices DeviceRepository, now func() time.Time) *RevokeDeviceUseCase {
	if now == nil {
		now = time.Now
	}
	return &RevokeDeviceUseCase{devices: devices, now: now}
}

type RevokeDeviceInput struct {
	OrgID    string
	DeviceID string
	Reason   string
}

// Execute revokes the device. Certificates it already produced stay in the
// registry: they are evidence of what a now-distrusted device did, and deleting
// them would destroy exactly what an investigation needs.
func (uc *RevokeDeviceUseCase) Execute(ctx context.Context, in RevokeDeviceInput) error {
	d, err := uc.devices.FindByID(ctx, in.DeviceID)
	if err != nil {
		return fmt.Errorf("revoke device: %w", err)
	}
	if d == nil || (in.OrgID != "" && d.OrgID != in.OrgID) {
		return fmt.Errorf("revoke device: %w", domain.ErrDeviceNotFound)
	}
	if err := uc.devices.Revoke(ctx, in.DeviceID, in.Reason, uc.now()); err != nil {
		return fmt.Errorf("revoke device: %w", err)
	}
	return nil
}

// AttestedCaptureUseCase is the gated certification path: only a capture that
// arrives signed by an enrolled device key, bound to a fresh challenge, becomes
// a certificate.
type AttestedCaptureUseCase struct {
	devices DeviceRepository
	nonces  NonceRepository
	certify CertifyRunner
	now     func() time.Time
}

func NewAttestedCaptureUseCase(devices DeviceRepository, nonces NonceRepository, certify CertifyRunner, now func() time.Time) *AttestedCaptureUseCase {
	if now == nil {
		now = time.Now
	}
	return &AttestedCaptureUseCase{devices: devices, nonces: nonces, certify: certify, now: now}
}

type AttestedCaptureInput struct {
	OrgID     string
	DeviceID  string
	Nonce     string
	Signature []byte
	Metadata  domain.CaptureMetadata
	Content   io.Reader
}

// Execute validates the capture end to end and delegates certification.
//
// The nonce is consumed before the signature is checked, on purpose: a failed
// signature still burns the challenge, so an attacker cannot hold one challenge
// open and grind signatures against it.
func (uc *AttestedCaptureUseCase) Execute(ctx context.Context, in AttestedCaptureInput) (*CertifyOutput, error) {
	rec := observability.FromContext(ctx)
	rec.SetPipeline("capture")

	if in.DeviceID == "" {
		return nil, fmt.Errorf("capture: device id is required")
	}
	if len(in.Signature) == 0 {
		return nil, fmt.Errorf("capture: signature is required")
	}
	if in.Content == nil {
		return nil, fmt.Errorf("capture: content is required")
	}

	nonce, err := observability.Stage(ctx, "consume_nonce", func(h observability.StageHandle) (*domain.CaptureNonce, error) {
		if !domain.ValidNonceFormat(in.Nonce) {
			return nil, domain.ErrNonceUnusable
		}
		n, e := uc.nonces.Consume(ctx, in.Nonce, uc.now())
		if e == nil {
			h.SetAttrs(observability.Attr{Key: "issued_at", Value: n.IssuedAt})
		}
		return n, e
	})
	if err != nil {
		return nil, fmt.Errorf("capture: %w", err)
	}
	if nonce.OrgID != in.OrgID {
		return nil, fmt.Errorf("capture: %w", domain.ErrNonceUnusable)
	}

	device, err := observability.Stage(ctx, "load_device", func(h observability.StageHandle) (*domain.Device, error) {
		d, e := uc.devices.FindByID(ctx, in.DeviceID)
		if e != nil {
			return nil, e
		}
		if d == nil || d.OrgID != in.OrgID {
			return nil, domain.ErrDeviceNotFound
		}
		h.SetAttrs(
			observability.Attr{Key: "platform", Value: string(d.Platform)},
			observability.Attr{Key: "attestation_level", Value: string(d.AttestationLevel)},
			observability.Attr{Key: "status", Value: string(d.Status)},
		)
		if !d.CanCapture() {
			return nil, domain.ErrDeviceRevoked
		}
		return d, nil
	})
	if err != nil {
		return nil, fmt.Errorf("capture: %w", err)
	}

	content, err := io.ReadAll(in.Content)
	if err != nil {
		return nil, fmt.Errorf("capture: reading content: %w", err)
	}

	contentHash, _ := observability.Stage(ctx, "sha256", func(h observability.StageHandle) (string, error) {
		hash, _ := domain.HashContent(bytes.NewReader(content))
		h.SetAttrs(
			observability.Attr{Key: "content_hash", Value: hash},
			observability.Attr{Key: "size_bytes", Value: len(content)},
		)
		return hash, nil
	})

	if err := observability.StageVoid(ctx, "verify_signature", func(h observability.StageHandle) error {
		payload := domain.CaptureSigningPayload(contentHash, nonce.Value, in.Metadata)
		h.SetAttrs(observability.Attr{Key: "payload_bytes", Value: len(payload)})
		return domain.VerifyCaptureSignature(device.PublicKey, payload, in.Signature)
	}); err != nil {
		// A signature failure here means the bytes are not the bytes the device
		// signed — a re-encode in the app, or tampering in transit.
		if errors.Is(err, domain.ErrCaptureSignature) {
			return nil, fmt.Errorf("capture: %w", domain.ErrCaptureSignature)
		}
		return nil, fmt.Errorf("capture: %w", err)
	}

	capturedAt := in.Metadata.CapturedAt.UTC()
	return uc.certify.Execute(ctx, CertifyInput{
		Content:    bytes.NewReader(content),
		Registrant: in.OrgID,
		OrgID:      in.OrgID,
		DeviceID:   device.ID,
		CapturedAt: &capturedAt,
	})
}
