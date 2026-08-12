package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
)

var orgNow = time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)

func TestValidPlan(t *testing.T) {
	for _, p := range []domain.Plan{domain.PlanDeveloper, domain.PlanGrowth, domain.PlanEnterprise} {
		if !domain.ValidPlan(p) {
			t.Errorf("%q should be a valid plan", p)
		}
	}
	for _, p := range []domain.Plan{"", "free", "Developer"} {
		if domain.ValidPlan(p) {
			t.Errorf("%q should be rejected", p)
		}
	}
}

func TestQuotaFor(t *testing.T) {
	if got := domain.QuotaFor(domain.PlanDeveloper, domain.OpAttestedCapture); got != 500 {
		t.Errorf("developer capture allowance = %d, want 500", got)
	}
	if got := domain.QuotaFor(domain.PlanGrowth, domain.OpAttestedCapture); got != domain.Unlimited {
		t.Errorf("growth capture allowance = %d, want unlimited", got)
	}

	// An unrecognised plan must fall back to the smallest tier. Falling back to
	// unlimited would turn a configuration typo into free service.
	if got := domain.QuotaFor("enterprise-plus", domain.OpAttestedCapture); got != 500 {
		t.Errorf("unknown plan allowance = %d, want the developer allowance", got)
	}
}

func TestBillingPeriod(t *testing.T) {
	if got := domain.BillingPeriod(orgNow); got != "2026-08" {
		t.Errorf("BillingPeriod = %q, want 2026-08", got)
	}

	// A local timestamp late on the last day of the month must not be counted
	// against the following period.
	loc := time.FixedZone("BRT", -3*60*60)
	endOfMonth := time.Date(2026, 8, 31, 23, 30, 0, 0, loc)
	if got := domain.BillingPeriod(endOfMonth); got != "2026-09" {
		t.Errorf("BillingPeriod = %q, want the UTC month 2026-09", got)
	}
}

func TestOrg_Active(t *testing.T) {
	if !(&domain.Org{Status: domain.OrgActive}).Active() {
		t.Error("an active org should be active")
	}
	if (&domain.Org{Status: domain.OrgSuspended}).Active() {
		t.Error("a suspended org must not be active")
	}
	var nilOrg *domain.Org
	if nilOrg.Active() {
		t.Error("a nil org must not be active")
	}
}

func TestNewAPIKey(t *testing.T) {
	key, plaintext, err := domain.NewAPIKey("org-1", "ci", orgNow)
	if err != nil {
		t.Fatalf("NewAPIKey: %v", err)
	}

	if !strings.HasPrefix(plaintext, domain.APIKeyPrefix) {
		t.Errorf("plaintext %q lacks the %q prefix", plaintext, domain.APIKeyPrefix)
	}
	if !domain.LooksLikeAPIKey(plaintext) {
		t.Error("a freshly minted key should pass the shape check")
	}

	// The plaintext must never be recoverable from what is stored.
	if strings.Contains(key.Hash, plaintext) || key.Hash == plaintext {
		t.Fatal("the stored hash must not contain the credential")
	}
	if key.Hash != domain.HashAPIKey(plaintext) {
		t.Error("the stored hash must be the lookup value for the plaintext")
	}
	if !strings.HasPrefix(plaintext, key.Prefix) {
		t.Errorf("prefix %q is not a prefix of the credential", key.Prefix)
	}
	if len(key.Prefix) >= len(plaintext) {
		t.Error("the display prefix must not reveal the whole credential")
	}
	if !key.Active() {
		t.Error("a new key should be active")
	}
}

func TestNewAPIKey_IsUnpredictable(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 200; i++ {
		_, plaintext, err := domain.NewAPIKey("org-1", "", orgNow)
		if err != nil {
			t.Fatal(err)
		}
		if seen[plaintext] {
			t.Fatal("credential issued twice")
		}
		seen[plaintext] = true
	}
}

func TestNewAPIKey_RequiresOrg(t *testing.T) {
	if _, _, err := domain.NewAPIKey("", "ci", orgNow); err == nil {
		t.Fatal("an org id is required")
	}
}

func TestAPIKey_Revoke(t *testing.T) {
	key, _, err := domain.NewAPIKey("org-1", "ci", orgNow)
	if err != nil {
		t.Fatal(err)
	}

	key.Revoke(orgNow.Add(time.Hour))

	if key.Active() {
		t.Error("a revoked key must not authenticate")
	}
	if key.RevokedAt == nil || !key.RevokedAt.Equal(orgNow.Add(time.Hour)) {
		t.Errorf("RevokedAt = %v", key.RevokedAt)
	}

	var nilKey *domain.APIKey
	if nilKey.Active() {
		t.Error("a nil key must not authenticate")
	}
}

func TestLooksLikeAPIKey(t *testing.T) {
	_, plaintext, err := domain.NewAPIKey("org-1", "", orgNow)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"issued credential", plaintext, true},
		{"empty", "", false},
		{"prefix only", domain.APIKeyPrefix, false},
		{"too short", domain.APIKeyPrefix + "abc", false},
		{"admin token shape", "some-admin-token-value-that-is-long", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := domain.LooksLikeAPIKey(tt.value); got != tt.want {
				t.Errorf("LooksLikeAPIKey(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestHashAPIKey_IsStable(t *testing.T) {
	if domain.HashAPIKey("alk_abc") != domain.HashAPIKey("alk_abc") {
		t.Error("hashing must be deterministic or lookups would fail")
	}
	if domain.HashAPIKey("alk_abc") == domain.HashAPIKey("alk_abd") {
		t.Error("distinct credentials must hash differently")
	}
}

func TestDisplayPrefix_ShortCredential(t *testing.T) {
	// A credential shorter than the display window must not panic on slicing.
	if got := domain.HashAPIKey("x"); got == "" {
		t.Fatal("hash should never be empty")
	}
	if !domain.LooksLikeAPIKey(domain.APIKeyPrefix + strings.Repeat("a", 20)) {
		t.Fatal("a long enough credential should pass the shape check")
	}
}
