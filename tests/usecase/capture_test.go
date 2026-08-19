package usecase_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

var captureNow = time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)

func fixedClock() func() time.Time { return func() time.Time { return captureNow } }

// --- mocks ------------------------------------------------------------------

type mockDeviceRepo struct {
	saveFn     func(ctx context.Context, d *domain.Device) error
	findFn     func(ctx context.Context, id string) (*domain.Device, error)
	findKeyFn  func(ctx context.Context, publicKey []byte) (*domain.Device, error)
	listFn     func(ctx context.Context, orgID string) ([]*domain.Device, error)
	revokeFn   func(ctx context.Context, id, reason string, at time.Time) error
	revokeCall struct {
		id     string
		reason string
	}
	saved []*domain.Device
}

func (m *mockDeviceRepo) Save(ctx context.Context, d *domain.Device) error {
	m.saved = append(m.saved, d)
	if m.saveFn == nil {
		d.ID = "device-1"
		return nil
	}
	return m.saveFn(ctx, d)
}

func (m *mockDeviceRepo) FindByPublicKey(ctx context.Context, publicKey []byte) (*domain.Device, error) {
	if m.findKeyFn == nil {
		return nil, nil
	}
	return m.findKeyFn(ctx, publicKey)
}

func (m *mockDeviceRepo) FindByID(ctx context.Context, id string) (*domain.Device, error) {
	if m.findFn == nil {
		return nil, nil
	}
	return m.findFn(ctx, id)
}

func (m *mockDeviceRepo) ListByOrg(ctx context.Context, orgID string) ([]*domain.Device, error) {
	if m.listFn == nil {
		return nil, nil
	}
	return m.listFn(ctx, orgID)
}

func (m *mockDeviceRepo) Revoke(ctx context.Context, id, reason string, at time.Time) error {
	m.revokeCall.id, m.revokeCall.reason = id, reason
	if m.revokeFn == nil {
		return nil
	}
	return m.revokeFn(ctx, id, reason, at)
}

type mockNonceRepo struct {
	saved     []domain.CaptureNonce
	saveErr   error
	consumeFn func(ctx context.Context, value string, now time.Time) (*domain.CaptureNonce, error)
	pruneFn   func(ctx context.Context, before time.Time) (int64, error)
}

func (m *mockNonceRepo) Save(_ context.Context, n domain.CaptureNonce) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.saved = append(m.saved, n)
	return nil
}

func (m *mockNonceRepo) Consume(ctx context.Context, value string, now time.Time) (*domain.CaptureNonce, error) {
	if m.consumeFn == nil {
		return nil, domain.ErrNonceUnusable
	}
	return m.consumeFn(ctx, value, now)
}

func (m *mockNonceRepo) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	if m.pruneFn == nil {
		return 0, nil
	}
	return m.pruneFn(ctx, before)
}

type mockAttestationVerifier struct {
	fn func(ctx context.Context, req domain.AttestationRequest) (*domain.AttestationEvidence, error)
}

func (m *mockAttestationVerifier) Verify(ctx context.Context, req domain.AttestationRequest) (*domain.AttestationEvidence, error) {
	return m.fn(ctx, req)
}

type mockCertifyRunner struct {
	in usecase.CertifyInput
	fn func(ctx context.Context, in usecase.CertifyInput) (*usecase.CertifyOutput, error)
}

func (m *mockCertifyRunner) Execute(ctx context.Context, in usecase.CertifyInput) (*usecase.CertifyOutput, error) {
	m.in = in
	if m.fn == nil {
		return &usecase.CertifyOutput{Certificate: &domain.Certificate{ID: "cert-1"}}, nil
	}
	return m.fn(ctx, in)
}

// --- helpers ----------------------------------------------------------------

func validNonce(t *testing.T, orgID string) domain.CaptureNonce {
	t.Helper()
	n, err := domain.NewCaptureNonce(orgID, 5*time.Minute, captureNow)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

type signer struct {
	key    *ecdsa.PrivateKey
	pubDER []byte
}

func newSigner(t *testing.T) signer {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return signer{key: k, pubDER: pub}
}

func (s signer) sign(t *testing.T, payload []byte) []byte {
	t.Helper()
	digest := sha256.Sum256(payload)
	sig, err := ecdsa.SignASN1(rand.Reader, s.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

// --- IssueNonceUseCase ------------------------------------------------------

func TestIssueNonceUseCase(t *testing.T) {
	repo := &mockNonceRepo{}
	uc := usecase.NewIssueNonceUseCase(repo, time.Minute, fixedClock())

	n, err := uc.Execute(context.Background(), "org-1")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(repo.saved) != 1 {
		t.Fatalf("saved %d nonces, want 1", len(repo.saved))
	}
	if repo.saved[0].Value != n.Value {
		t.Error("the persisted challenge must be the one returned to the caller")
	}
	if !n.ExpiresAt.Equal(captureNow.Add(time.Minute)) {
		t.Errorf("ExpiresAt = %v, want a one-minute window", n.ExpiresAt)
	}
}

func TestIssueNonceUseCase_Defaults(t *testing.T) {
	uc := usecase.NewIssueNonceUseCase(&mockNonceRepo{}, 0, nil)

	n, err := uc.Execute(context.Background(), "org-1")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := n.ExpiresAt.Sub(n.IssuedAt); got != 5*time.Minute {
		t.Errorf("default ttl = %v, want 5m", got)
	}
}

func TestIssueNonceUseCase_Errors(t *testing.T) {
	t.Run("rejects a missing org", func(t *testing.T) {
		uc := usecase.NewIssueNonceUseCase(&mockNonceRepo{}, time.Minute, fixedClock())
		if _, err := uc.Execute(context.Background(), ""); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("propagates a storage failure", func(t *testing.T) {
		uc := usecase.NewIssueNonceUseCase(&mockNonceRepo{saveErr: errors.New("db down")}, time.Minute, fixedClock())
		if _, err := uc.Execute(context.Background(), "org-1"); err == nil {
			t.Fatal("expected an error")
		}
	})
}

// --- EnrollDeviceUseCase ----------------------------------------------------

func TestEnrollDeviceUseCase_HappyPath(t *testing.T) {
	nonce := validNonce(t, "org-1")
	devices := &mockDeviceRepo{}
	nonces := &mockNonceRepo{consumeFn: func(_ context.Context, v string, _ time.Time) (*domain.CaptureNonce, error) {
		return &nonce, nil
	}}

	var sawChallenge []byte
	verifier := &mockAttestationVerifier{fn: func(_ context.Context, req domain.AttestationRequest) (*domain.AttestationEvidence, error) {
		sawChallenge = req.Challenge
		return &domain.AttestationEvidence{
			PublicKeyDER: []byte("public-key"),
			Level:        domain.AttestationStrongBox,
		}, nil
	}}

	uc := usecase.NewEnrollDeviceUseCase(devices, nonces, verifier, fixedClock())
	got, err := uc.Execute(context.Background(), usecase.EnrollDeviceInput{
		OrgID:     "org-1",
		Platform:  domain.PlatformAndroid,
		Nonce:     nonce.Value,
		CertChain: [][]byte{{1, 2, 3}},
		Model:     "Pixel 8",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if string(sawChallenge) != nonce.Value {
		t.Error("the attestation must be checked against the issued challenge")
	}
	if got.Status != domain.DeviceActive {
		t.Errorf("Status = %q, want active", got.Status)
	}
	if got.AttestationLevel != domain.AttestationStrongBox {
		t.Errorf("AttestationLevel = %q, want strongbox", got.AttestationLevel)
	}
	if string(got.PublicKey) != "public-key" {
		t.Error("the attested key must be the one stored")
	}
}

func TestEnrollDeviceUseCase_Rejections(t *testing.T) {
	nonce := validNonce(t, "org-1")

	okVerifier := &mockAttestationVerifier{fn: func(_ context.Context, _ domain.AttestationRequest) (*domain.AttestationEvidence, error) {
		return &domain.AttestationEvidence{PublicKeyDER: []byte("k"), Level: domain.AttestationTEE}, nil
	}}

	tests := []struct {
		name     string
		nonces   *mockNonceRepo
		verifier *mockAttestationVerifier
		devices  *mockDeviceRepo
		in       usecase.EnrollDeviceInput
		wantErr  string
	}{
		{
			name:     "unsupported platform",
			nonces:   &mockNonceRepo{},
			verifier: okVerifier,
			in:       usecase.EnrollDeviceInput{OrgID: "org-1", Platform: "symbian", Nonce: nonce.Value, CertChain: [][]byte{{1}}},
			wantErr:  "unsupported platform",
		},
		{
			name:     "empty chain",
			nonces:   &mockNonceRepo{},
			verifier: okVerifier,
			in:       usecase.EnrollDeviceInput{OrgID: "org-1", Platform: domain.PlatformAndroid, Nonce: nonce.Value},
			wantErr:  "certificate chain is required",
		},
		{
			name:     "malformed nonce never reaches the database",
			nonces:   &mockNonceRepo{},
			verifier: okVerifier,
			in:       usecase.EnrollDeviceInput{OrgID: "org-1", Platform: domain.PlatformAndroid, Nonce: "short", CertChain: [][]byte{{1}}},
			wantErr:  "not usable",
		},
		{
			name: "spent nonce",
			nonces: &mockNonceRepo{consumeFn: func(_ context.Context, _ string, _ time.Time) (*domain.CaptureNonce, error) {
				return nil, domain.ErrNonceUnusable
			}},
			verifier: okVerifier,
			in:       usecase.EnrollDeviceInput{OrgID: "org-1", Platform: domain.PlatformAndroid, Nonce: nonce.Value, CertChain: [][]byte{{1}}},
			wantErr:  "not usable",
		},
		{
			name: "nonce belonging to another tenant",
			nonces: &mockNonceRepo{consumeFn: func(_ context.Context, _ string, _ time.Time) (*domain.CaptureNonce, error) {
				other := validNonce(t, "org-2")
				return &other, nil
			}},
			verifier: okVerifier,
			in:       usecase.EnrollDeviceInput{OrgID: "org-1", Platform: domain.PlatformAndroid, Nonce: nonce.Value, CertChain: [][]byte{{1}}},
			wantErr:  "not usable",
		},
		{
			name: "attestation rejected",
			nonces: &mockNonceRepo{consumeFn: func(_ context.Context, _ string, _ time.Time) (*domain.CaptureNonce, error) {
				return &nonce, nil
			}},
			verifier: &mockAttestationVerifier{fn: func(_ context.Context, _ domain.AttestationRequest) (*domain.AttestationEvidence, error) {
				return nil, errors.New("attestation rejected: bootloader is unlocked")
			}},
			in:      usecase.EnrollDeviceInput{OrgID: "org-1", Platform: domain.PlatformAndroid, Nonce: nonce.Value, CertChain: [][]byte{{1}}},
			wantErr: "bootloader is unlocked",
		},
		{
			name: "storage failure",
			nonces: &mockNonceRepo{consumeFn: func(_ context.Context, _ string, _ time.Time) (*domain.CaptureNonce, error) {
				return &nonce, nil
			}},
			verifier: okVerifier,
			devices:  &mockDeviceRepo{saveFn: func(_ context.Context, _ *domain.Device) error { return errors.New("db down") }},
			in:       usecase.EnrollDeviceInput{OrgID: "org-1", Platform: domain.PlatformAndroid, Nonce: nonce.Value, CertChain: [][]byte{{1}}},
			wantErr:  "saving device",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			devices := tt.devices
			if devices == nil {
				devices = &mockDeviceRepo{}
			}
			uc := usecase.NewEnrollDeviceUseCase(devices, tt.nonces, tt.verifier, fixedClock())

			_, err := uc.Execute(context.Background(), tt.in)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not mention %q", err, tt.wantErr)
			}
		})
	}
}

// --- RevokeDeviceUseCase ----------------------------------------------------

func TestRevokeDeviceUseCase(t *testing.T) {
	t.Run("revokes an owned device", func(t *testing.T) {
		devices := &mockDeviceRepo{findFn: func(_ context.Context, id string) (*domain.Device, error) {
			return &domain.Device{ID: id, OrgID: "org-1", Status: domain.DeviceActive}, nil
		}}
		uc := usecase.NewRevokeDeviceUseCase(devices, fixedClock())

		if err := uc.Execute(context.Background(), usecase.RevokeDeviceInput{
			OrgID: "org-1", DeviceID: "device-1", Reason: "lost",
		}); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if devices.revokeCall.id != "device-1" || devices.revokeCall.reason != "lost" {
			t.Errorf("revoke called with %+v", devices.revokeCall)
		}
	})

	t.Run("refuses another tenant's device", func(t *testing.T) {
		devices := &mockDeviceRepo{findFn: func(_ context.Context, id string) (*domain.Device, error) {
			return &domain.Device{ID: id, OrgID: "org-2"}, nil
		}}
		uc := usecase.NewRevokeDeviceUseCase(devices, fixedClock())

		err := uc.Execute(context.Background(), usecase.RevokeDeviceInput{OrgID: "org-1", DeviceID: "device-1"})
		if !errors.Is(err, domain.ErrDeviceNotFound) {
			t.Fatalf("error = %v, want ErrDeviceNotFound", err)
		}
	})

	t.Run("reports an unknown device", func(t *testing.T) {
		uc := usecase.NewRevokeDeviceUseCase(&mockDeviceRepo{}, fixedClock())
		err := uc.Execute(context.Background(), usecase.RevokeDeviceInput{OrgID: "org-1", DeviceID: "nope"})
		if !errors.Is(err, domain.ErrDeviceNotFound) {
			t.Fatalf("error = %v, want ErrDeviceNotFound", err)
		}
	})

	t.Run("propagates lookup and update failures", func(t *testing.T) {
		lookupErr := &mockDeviceRepo{findFn: func(_ context.Context, _ string) (*domain.Device, error) {
			return nil, errors.New("db down")
		}}
		uc := usecase.NewRevokeDeviceUseCase(lookupErr, nil)
		if err := uc.Execute(context.Background(), usecase.RevokeDeviceInput{DeviceID: "d"}); err == nil {
			t.Error("expected the lookup failure to surface")
		}

		updateErr := &mockDeviceRepo{
			findFn: func(_ context.Context, id string) (*domain.Device, error) {
				return &domain.Device{ID: id, OrgID: "org-1"}, nil
			},
			revokeFn: func(_ context.Context, _, _ string, _ time.Time) error { return errors.New("db down") },
		}
		uc = usecase.NewRevokeDeviceUseCase(updateErr, fixedClock())
		if err := uc.Execute(context.Background(), usecase.RevokeDeviceInput{OrgID: "org-1", DeviceID: "d"}); err == nil {
			t.Error("expected the update failure to surface")
		}
	})
}

// --- AttestedCaptureUseCase -------------------------------------------------

type captureFixture struct {
	uc      *usecase.AttestedCaptureUseCase
	runner  *mockCertifyRunner
	nonce   domain.CaptureNonce
	sig     signer
	content []byte
	md      domain.CaptureMetadata
}

func newCaptureFixture(t *testing.T, mutate func(*mockDeviceRepo, *mockNonceRepo)) captureFixture {
	t.Helper()

	nonce := validNonce(t, "org-1")
	sig := newSigner(t)

	devices := &mockDeviceRepo{findFn: func(_ context.Context, id string) (*domain.Device, error) {
		return &domain.Device{
			ID: id, OrgID: "org-1", Status: domain.DeviceActive,
			PublicKey: sig.pubDER, Platform: domain.PlatformAndroid,
			AttestationLevel: domain.AttestationTEE,
		}, nil
	}}
	nonces := &mockNonceRepo{consumeFn: func(_ context.Context, _ string, _ time.Time) (*domain.CaptureNonce, error) {
		return &nonce, nil
	}}
	if mutate != nil {
		mutate(devices, nonces)
	}

	runner := &mockCertifyRunner{}
	return captureFixture{
		uc:      usecase.NewAttestedCaptureUseCase(devices, nonces, runner, fixedClock()),
		runner:  runner,
		nonce:   nonce,
		sig:     sig,
		content: []byte("image bytes"),
		md: domain.CaptureMetadata{
			CapturedAt: captureNow, Model: "Pixel 8", OSVersion: "14", AppVersion: "1.0.0",
		},
	}
}

func (f captureFixture) input(t *testing.T) usecase.AttestedCaptureInput {
	t.Helper()
	hash, err := domain.HashContent(strings.NewReader(string(f.content)))
	if err != nil {
		t.Fatal(err)
	}
	payload := domain.CaptureSigningPayload(hash, f.nonce.Value, f.md)
	return usecase.AttestedCaptureInput{
		OrgID:     "org-1",
		DeviceID:  "device-1",
		Nonce:     f.nonce.Value,
		Signature: f.sig.sign(t, payload),
		Metadata:  f.md,
		Content:   strings.NewReader(string(f.content)),
	}
}

func TestAttestedCaptureUseCase_HappyPath(t *testing.T) {
	f := newCaptureFixture(t, nil)

	out, err := f.uc.Execute(context.Background(), f.input(t))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Certificate.ID != "cert-1" {
		t.Errorf("certificate id = %q", out.Certificate.ID)
	}

	// Provenance must reach the certificate, or the capture proved nothing.
	if f.runner.in.DeviceID != "device-1" {
		t.Errorf("DeviceID = %q, want device-1", f.runner.in.DeviceID)
	}
	if f.runner.in.OrgID != "org-1" {
		t.Errorf("OrgID = %q, want org-1", f.runner.in.OrgID)
	}
	if f.runner.in.CapturedAt == nil || !f.runner.in.CapturedAt.Equal(captureNow) {
		t.Errorf("CapturedAt = %v, want %v", f.runner.in.CapturedAt, captureNow)
	}
}

func TestAttestedCaptureUseCase_RejectsTamperedContent(t *testing.T) {
	f := newCaptureFixture(t, nil)

	in := f.input(t)
	// Same signature, different bytes: exactly what a re-encode in the app or
	// tampering in transit looks like.
	in.Content = strings.NewReader("different image bytes")

	_, err := f.uc.Execute(context.Background(), in)
	if !errors.Is(err, domain.ErrCaptureSignature) {
		t.Fatalf("error = %v, want ErrCaptureSignature", err)
	}
}

func TestAttestedCaptureUseCase_RejectsTamperedMetadata(t *testing.T) {
	f := newCaptureFixture(t, nil)

	in := f.input(t)
	in.Metadata.Model = "Some Other Phone"

	_, err := f.uc.Execute(context.Background(), in)
	if !errors.Is(err, domain.ErrCaptureSignature) {
		t.Fatalf("error = %v, want ErrCaptureSignature", err)
	}
}

func TestAttestedCaptureUseCase_RejectsRevokedDevice(t *testing.T) {
	f := newCaptureFixture(t, func(d *mockDeviceRepo, _ *mockNonceRepo) {
		d.findFn = func(_ context.Context, id string) (*domain.Device, error) {
			return &domain.Device{ID: id, OrgID: "org-1", Status: domain.DeviceRevoked}, nil
		}
	})

	_, err := f.uc.Execute(context.Background(), f.input(t))
	if !errors.Is(err, domain.ErrDeviceRevoked) {
		t.Fatalf("error = %v, want ErrDeviceRevoked", err)
	}
}

func TestAttestedCaptureUseCase_RejectsForeignDevice(t *testing.T) {
	f := newCaptureFixture(t, func(d *mockDeviceRepo, _ *mockNonceRepo) {
		d.findFn = func(_ context.Context, id string) (*domain.Device, error) {
			return &domain.Device{ID: id, OrgID: "org-2", Status: domain.DeviceActive}, nil
		}
	})

	_, err := f.uc.Execute(context.Background(), f.input(t))
	if !errors.Is(err, domain.ErrDeviceNotFound) {
		t.Fatalf("error = %v, want ErrDeviceNotFound", err)
	}
}

// TestAttestedCaptureUseCase_BurnsNonceBeforeVerifying pins a deliberate
// ordering choice: a bad signature still spends the challenge, so an attacker
// cannot hold one nonce open and grind signatures against it.
func TestAttestedCaptureUseCase_BurnsNonceBeforeVerifying(t *testing.T) {
	var consumed int
	f := newCaptureFixture(t, func(_ *mockDeviceRepo, n *mockNonceRepo) {
		nonce := validNonce(t, "org-1")
		n.consumeFn = func(_ context.Context, _ string, _ time.Time) (*domain.CaptureNonce, error) {
			consumed++
			return &nonce, nil
		}
	})

	in := f.input(t)
	in.Signature = []byte{0x30, 0x06, 0x02, 0x01, 0x01, 0x02, 0x01, 0x01} // well-formed DER, wrong signature

	if _, err := f.uc.Execute(context.Background(), in); err == nil {
		t.Fatal("expected the capture to be rejected")
	}
	if consumed != 1 {
		t.Errorf("nonce consumed %d times, want 1 even on failure", consumed)
	}
}

func TestAttestedCaptureUseCase_InputValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*usecase.AttestedCaptureInput)
		wantErr string
	}{
		{"device id required", func(in *usecase.AttestedCaptureInput) { in.DeviceID = "" }, "device id is required"},
		{"signature required", func(in *usecase.AttestedCaptureInput) { in.Signature = nil }, "signature is required"},
		{"content required", func(in *usecase.AttestedCaptureInput) { in.Content = nil }, "content is required"},
		{"malformed nonce", func(in *usecase.AttestedCaptureInput) { in.Nonce = "nope" }, "not usable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newCaptureFixture(t, nil)
			in := f.input(t)
			tt.mutate(&in)

			_, err := f.uc.Execute(context.Background(), in)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestAttestedCaptureUseCase_RejectsForeignNonce(t *testing.T) {
	f := newCaptureFixture(t, func(_ *mockDeviceRepo, n *mockNonceRepo) {
		n.consumeFn = func(_ context.Context, _ string, _ time.Time) (*domain.CaptureNonce, error) {
			other := validNonce(t, "org-2")
			return &other, nil
		}
	})

	_, err := f.uc.Execute(context.Background(), f.input(t))
	if !errors.Is(err, domain.ErrNonceUnusable) {
		t.Fatalf("error = %v, want ErrNonceUnusable", err)
	}
}

func TestAttestedCaptureUseCase_PropagatesLookupFailure(t *testing.T) {
	f := newCaptureFixture(t, func(d *mockDeviceRepo, _ *mockNonceRepo) {
		d.findFn = func(_ context.Context, _ string) (*domain.Device, error) {
			return nil, errors.New("db down")
		}
	})

	if _, err := f.uc.Execute(context.Background(), f.input(t)); err == nil {
		t.Fatal("expected the lookup failure to surface")
	}
}

func TestCaptureUseCases_DefaultClock(t *testing.T) {
	// A nil clock must fall back to wall time rather than a zero Time, which
	// would make every nonce look expired and every capture fail.
	nonce := validNonce(t, "org-1")
	nonces := &mockNonceRepo{consumeFn: func(_ context.Context, _ string, _ time.Time) (*domain.CaptureNonce, error) {
		return &nonce, nil
	}}
	verifier := &mockAttestationVerifier{fn: func(_ context.Context, _ domain.AttestationRequest) (*domain.AttestationEvidence, error) {
		return &domain.AttestationEvidence{PublicKeyDER: []byte("k"), Level: domain.AttestationTEE}, nil
	}}

	enroll := usecase.NewEnrollDeviceUseCase(&mockDeviceRepo{}, nonces, verifier, nil)
	got, err := enroll.Execute(context.Background(), usecase.EnrollDeviceInput{
		OrgID: "org-1", Platform: domain.PlatformAndroid, Nonce: nonce.Value, CertChain: [][]byte{{1}},
	})
	if err != nil {
		t.Fatalf("enroll with default clock: %v", err)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should come from the wall clock, not the zero value")
	}

	capture := usecase.NewAttestedCaptureUseCase(&mockDeviceRepo{}, nonces, &mockCertifyRunner{}, nil)
	if _, err := capture.Execute(context.Background(), usecase.AttestedCaptureInput{
		OrgID: "org-1", DeviceID: "d", Signature: []byte{1}, Nonce: nonce.Value, Content: strings.NewReader("x"),
	}); !errors.Is(err, domain.ErrDeviceNotFound) {
		t.Fatalf("error = %v, want ErrDeviceNotFound", err)
	}
}

func TestAttestedCaptureUseCase_NonceRepoFailure(t *testing.T) {
	f := newCaptureFixture(t, func(_ *mockDeviceRepo, n *mockNonceRepo) {
		n.consumeFn = func(_ context.Context, _ string, _ time.Time) (*domain.CaptureNonce, error) {
			return nil, errors.New("db down")
		}
	})
	if _, err := f.uc.Execute(context.Background(), f.input(t)); err == nil {
		t.Fatal("expected the storage failure to surface")
	}
}

func TestNonceRepoPruneIsWiredThroughTheMock(t *testing.T) {
	repo := &mockNonceRepo{pruneFn: func(_ context.Context, _ time.Time) (int64, error) { return 7, nil }}
	n, err := repo.DeleteExpired(context.Background(), captureNow)
	if err != nil || n != 7 {
		t.Fatalf("DeleteExpired = (%d, %v)", n, err)
	}
}

// TestAttestedCaptureUseCase_UnusableDeviceKey covers the case where the stored
// key cannot be parsed at all, which is a different failure from a signature
// that simply does not match.
func TestAttestedCaptureUseCase_UnusableDeviceKey(t *testing.T) {
	f := newCaptureFixture(t, func(d *mockDeviceRepo, _ *mockNonceRepo) {
		d.findFn = func(_ context.Context, id string) (*domain.Device, error) {
			return &domain.Device{
				ID: id, OrgID: "org-1", Status: domain.DeviceActive,
				PublicKey: []byte("this is not a public key"),
			}, nil
		}
	})

	_, err := f.uc.Execute(context.Background(), f.input(t))
	if err == nil {
		t.Fatal("expected the capture to be rejected")
	}
	if errors.Is(err, domain.ErrCaptureSignature) {
		t.Error("an unparseable key is a device-record problem, not a signature mismatch")
	}
	if !strings.Contains(err.Error(), "device public key") {
		t.Errorf("error %q should name the cause", err)
	}
}

// --- one hardware key, one device record -------------------------------------
//
// A device's identity is its attested key, not the row it happens to occupy.
// Without that binding, revocation only ever applied to a record the device
// could replace by enrolling again.

func enrolFixture(t *testing.T, devices *mockDeviceRepo, key string) (*usecase.EnrollDeviceUseCase, usecase.EnrollDeviceInput) {
	t.Helper()
	nonce := validNonce(t, "org-1")
	nonces := &mockNonceRepo{consumeFn: func(_ context.Context, _ string, _ time.Time) (*domain.CaptureNonce, error) {
		return &nonce, nil
	}}
	verifier := &mockAttestationVerifier{fn: func(_ context.Context, _ domain.AttestationRequest) (*domain.AttestationEvidence, error) {
		return &domain.AttestationEvidence{PublicKeyDER: []byte(key), Level: domain.AttestationTEE}, nil
	}}

	uc := usecase.NewEnrollDeviceUseCase(devices, nonces, verifier, fixedClock())
	in := usecase.EnrollDeviceInput{
		OrgID:     "org-1",
		Platform:  domain.PlatformAndroid,
		Nonce:     nonce.Value,
		CertChain: [][]byte{{1, 2, 3}},
	}
	return uc, in
}

func TestEnrollDeviceUseCase_RevokedKeyCannotEnrolAgain(t *testing.T) {
	devices := &mockDeviceRepo{
		findKeyFn: func(_ context.Context, key []byte) (*domain.Device, error) {
			return &domain.Device{
				ID:     "device-old",
				OrgID:  "org-1",
				Status: domain.DeviceRevoked,
			}, nil
		},
	}
	uc, in := enrolFixture(t, devices, "hardware-key")

	_, err := uc.Execute(context.Background(), in)
	if !errors.Is(err, domain.ErrDeviceRevoked) {
		t.Fatalf("error = %v, want ErrDeviceRevoked", err)
	}
	if len(devices.saved) != 0 {
		t.Fatal("a revoked key must not get a fresh active record")
	}
}

func TestEnrollDeviceUseCase_KeyEnrolledByAnotherOrgIsRefused(t *testing.T) {
	devices := &mockDeviceRepo{
		findKeyFn: func(_ context.Context, key []byte) (*domain.Device, error) {
			return &domain.Device{ID: "device-other", OrgID: "org-2", Status: domain.DeviceActive}, nil
		},
	}
	uc, in := enrolFixture(t, devices, "hardware-key")

	_, err := uc.Execute(context.Background(), in)
	if !errors.Is(err, domain.ErrDeviceKeyInUse) {
		t.Fatalf("error = %v, want ErrDeviceKeyInUse", err)
	}
	if len(devices.saved) != 0 {
		t.Fatal("a key belonging to another tenant must not be re-bound")
	}
}

// Re-enrolling a key the same org already has is not an attack — it is an SDK
// that lost its device id. It gets the existing record back, not a duplicate.
func TestEnrollDeviceUseCase_ReenrolmentIsIdempotent(t *testing.T) {
	existing := &domain.Device{ID: "device-1", OrgID: "org-1", Status: domain.DeviceActive}
	devices := &mockDeviceRepo{
		findKeyFn: func(_ context.Context, key []byte) (*domain.Device, error) { return existing, nil },
	}
	uc, in := enrolFixture(t, devices, "hardware-key")

	got, err := uc.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.ID != existing.ID {
		t.Errorf("device id = %q, want the existing %q", got.ID, existing.ID)
	}
	if len(devices.saved) != 0 {
		t.Fatal("re-enrolment must not create a second record for one key")
	}
}

func TestEnrollDeviceUseCase_PropagatesKeyLookupFailure(t *testing.T) {
	devices := &mockDeviceRepo{
		findKeyFn: func(_ context.Context, key []byte) (*domain.Device, error) {
			return nil, errors.New("db down")
		},
	}
	uc, in := enrolFixture(t, devices, "hardware-key")

	_, err := uc.Execute(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "looking up attested key") {
		t.Fatalf("error = %v, want the lookup failure to surface", err)
	}
	if len(devices.saved) != 0 {
		t.Fatal("an unreadable registry must not fall through to enrolment")
	}
}
