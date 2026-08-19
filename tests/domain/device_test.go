package domain_test

import (
	"testing"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
)

func TestAttestationLevel_AtLeast(t *testing.T) {
	tests := []struct {
		name string
		got  domain.AttestationLevel
		min  domain.AttestationLevel
		want bool
	}{
		{"strongbox meets tee", domain.AttestationStrongBox, domain.AttestationTEE, true},
		{"tee meets tee", domain.AttestationTEE, domain.AttestationTEE, true},
		{"tee does not meet strongbox", domain.AttestationTEE, domain.AttestationStrongBox, false},
		{"software does not meet tee", domain.AttestationSoftware, domain.AttestationTEE, false},
		{"software meets software", domain.AttestationSoftware, domain.AttestationSoftware, true},
		// An unrecognised level must never satisfy a hardware policy, or a
		// typo in configuration would silently open the gate.
		{"unknown level fails a hardware policy", domain.AttestationLevel("magic"), domain.AttestationTEE, false},
		// Ranking below everything means below software too. A plain map index
		// would have let an unknown value tie with software at rank zero.
		{"unknown level fails even the weakest policy", domain.AttestationLevel("magic"), domain.AttestationSoftware, false},
		{"empty level fails the weakest policy", domain.AttestationLevel(""), domain.AttestationSoftware, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.got.AtLeast(tt.min); got != tt.want {
				t.Errorf("%q.AtLeast(%q) = %v, want %v", tt.got, tt.min, got, tt.want)
			}
		})
	}
}

func TestValidPlatform(t *testing.T) {
	for _, p := range []domain.Platform{domain.PlatformAndroid, domain.PlatformIOS} {
		if !domain.ValidPlatform(p) {
			t.Errorf("%q should be valid", p)
		}
	}
	for _, p := range []domain.Platform{"", "windows", "Android"} {
		if domain.ValidPlatform(p) {
			t.Errorf("%q should be rejected", p)
		}
	}
}

func TestDevice_CanCapture(t *testing.T) {
	tests := []struct {
		name string
		dev  *domain.Device
		want bool
	}{
		{"active device captures", &domain.Device{Status: domain.DeviceActive}, true},
		{"revoked device does not", &domain.Device{Status: domain.DeviceRevoked}, false},
		{"unknown status does not", &domain.Device{Status: ""}, false},
		{"nil device does not", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.dev.CanCapture(); got != tt.want {
				t.Errorf("CanCapture() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDevice_Revoke(t *testing.T) {
	at := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	d := &domain.Device{Status: domain.DeviceActive}

	d.Revoke("key extracted from a rooted handset", at)

	if d.Status != domain.DeviceRevoked {
		t.Errorf("Status = %q, want revoked", d.Status)
	}
	if d.CanCapture() {
		t.Error("a revoked device must not capture")
	}
	if d.RevokedAt == nil || !d.RevokedAt.Equal(at) {
		t.Errorf("RevokedAt = %v, want %v", d.RevokedAt, at)
	}
	if d.RevocationReason == "" {
		t.Error("the reason must be retained for audit")
	}
}

func TestCertificate_ProvenanceFlags(t *testing.T) {
	attested := &domain.Certificate{DeviceID: "device-1", TxHash: "0xabc"}
	if !attested.Attested() {
		t.Error("a certificate with a device should report as attested")
	}
	if !attested.Anchored() {
		t.Error("a certificate with a tx hash should report as anchored")
	}

	plain := &domain.Certificate{}
	if plain.Attested() {
		t.Error("an upload without a device is not attested")
	}
	if plain.Anchored() {
		t.Error("a certificate without a tx hash is not anchored")
	}

	var nilCert *domain.Certificate
	if nilCert.Attested() || nilCert.Anchored() {
		t.Error("nil certificate must report neither")
	}
}
