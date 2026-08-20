package domain_test

import (
	"testing"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
)

var nonceNow = time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)

func TestNewCaptureNonce(t *testing.T) {
	n, err := domain.NewCaptureNonce("org-1", 5*time.Minute, nonceNow)
	if err != nil {
		t.Fatalf("NewCaptureNonce: %v", err)
	}

	if !domain.ValidNonceFormat(n.Value) {
		t.Errorf("issued value %q does not satisfy the format check", n.Value)
	}
	if n.OrgID != "org-1" {
		t.Errorf("OrgID = %q, want org-1", n.OrgID)
	}
	if !n.ExpiresAt.Equal(nonceNow.Add(5 * time.Minute)) {
		t.Errorf("ExpiresAt = %v, want %v", n.ExpiresAt, nonceNow.Add(5*time.Minute))
	}
	if n.Consumed() {
		t.Error("a freshly issued nonce is not consumed")
	}
	if !n.Usable(nonceNow) {
		t.Error("a freshly issued nonce should be usable")
	}
}

func TestNewCaptureNonce_IsUnpredictable(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 200; i++ {
		n, err := domain.NewCaptureNonce("org-1", time.Minute, nonceNow)
		if err != nil {
			t.Fatal(err)
		}
		if seen[n.Value] {
			t.Fatalf("challenge %q issued twice", n.Value)
		}
		seen[n.Value] = true
	}
}

func TestNewCaptureNonce_Validation(t *testing.T) {
	if _, err := domain.NewCaptureNonce("", time.Minute, nonceNow); err == nil {
		t.Error("an org id is required")
	}
	if _, err := domain.NewCaptureNonce("org-1", 0, nonceNow); err == nil {
		t.Error("a positive ttl is required")
	}
	if _, err := domain.NewCaptureNonce("org-1", -time.Minute, nonceNow); err == nil {
		t.Error("a negative ttl is required to fail")
	}
}

func TestCaptureNonce_Lifecycle(t *testing.T) {
	n, err := domain.NewCaptureNonce("org-1", time.Minute, nonceNow)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("expires at the boundary", func(t *testing.T) {
		if n.Expired(nonceNow.Add(59 * time.Second)) {
			t.Error("still inside the window")
		}
		// The boundary is exclusive: at exactly ExpiresAt the nonce is spent.
		if !n.Expired(nonceNow.Add(time.Minute)) {
			t.Error("expiry should be inclusive of the deadline")
		}
		if n.Usable(nonceNow.Add(time.Minute)) {
			t.Error("an expired nonce is not usable")
		}
	})

	t.Run("consumption is terminal", func(t *testing.T) {
		consumed := n
		at := nonceNow.Add(time.Second)
		consumed.ConsumedAt = &at

		if !consumed.Consumed() {
			t.Error("expected Consumed to report true")
		}
		if consumed.Usable(nonceNow.Add(2 * time.Second)) {
			t.Error("a spent nonce must not be reusable even inside its window")
		}
	})
}

func TestValidNonceFormat(t *testing.T) {
	valid, err := domain.NewCaptureNonce("org-1", time.Minute, nonceNow)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"issued value", valid.Value, true},
		{"empty", "", false},
		{"too short", "abcd", false},
		{"right length but not hex", string(make([]byte, 0, 64)) + "zz" + valid.Value[2:], false},
		{"too long", valid.Value + "00", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := domain.ValidNonceFormat(tt.value); got != tt.want {
				t.Errorf("ValidNonceFormat(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}
