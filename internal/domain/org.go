package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Plan is an organisation's commercial tier.
type Plan string

const (
	PlanDeveloper  Plan = "developer"
	PlanGrowth     Plan = "growth"
	PlanEnterprise Plan = "enterprise"
)

// ValidPlan reports whether p is a plan the registry sells.
func ValidPlan(p Plan) bool {
	switch p {
	case PlanDeveloper, PlanGrowth, PlanEnterprise:
		return true
	}
	return false
}

// Operation is a metered action.
type Operation string

const (
	// OpAttestedCapture is the billable one: a hardware-attested capture that
	// became a certificate.
	OpAttestedCapture Operation = "attested_capture"
	// OpVerify is metered but generously free — verification is what makes
	// certificates worth buying, so it is never the thing that blocks a
	// customer.
	OpVerify Operation = "verify"
)

// Unlimited marks a plan allowance with no ceiling. It is negative so that the
// zero value — what a map miss yields — means "no allowance at all" rather than
// "uncapped". A lookup that falls through a gap in the table must not hand out
// free service.
const Unlimited = -1

// planQuotas is the monthly allowance per plan and operation. Unlimited means
// the operation is not capped; billing still counts it.
var planQuotas = map[Plan]map[Operation]int{
	PlanDeveloper: {
		OpAttestedCapture: 500,
		OpVerify:          10_000,
	},
	PlanGrowth: {
		OpAttestedCapture: Unlimited,
		OpVerify:          Unlimited,
	},
	PlanEnterprise: {
		OpAttestedCapture: Unlimited,
		OpVerify:          Unlimited,
	},
}

// QuotaFor returns the monthly allowance for a plan and operation.
//
// Both lookups fail towards the restrictive end. An unknown plan gets the
// developer allowance — the smallest tier, never an uncapped one. An operation
// the plan does not list gets no allowance at all, so adding a metered
// operation without adding it to planQuotas blocks it loudly instead of
// serving it for free.
func QuotaFor(p Plan, op Operation) int {
	limits, ok := planQuotas[p]
	if !ok {
		limits = planQuotas[PlanDeveloper]
	}
	limit, ok := limits[op]
	if !ok {
		return 0
	}
	return limit
}

// BillingPeriod is the calendar month, in UTC, that at falls into. Usage
// counters are keyed by it.
func BillingPeriod(at time.Time) string {
	return at.UTC().Format("2006-01")
}

// OrgStatus is a tenant's standing.
type OrgStatus string

const (
	OrgActive    OrgStatus = "active"
	OrgSuspended OrgStatus = "suspended"
)

// Org is a paying tenant. Devices, certificates, API keys and usage all hang
// off it.
type Org struct {
	ID        string
	Name      string
	Plan      Plan
	Status    OrgStatus
	CreatedAt time.Time
}

// Active reports whether the org may use the API.
func (o *Org) Active() bool { return o != nil && o.Status == OrgActive }

// APIKeyPrefix marks credentials so they are recognisable in logs and secret
// scanners.
const APIKeyPrefix = "alk_"

// apiKeyBytes is the entropy behind a credential.
const apiKeyBytes = 32

// APIKey is a credential belonging to an org.
//
// Only the hash is ever persisted. A database dump therefore yields no usable
// credentials, and the plaintext exists exactly once: in the response to the
// call that created it.
type APIKey struct {
	ID     string
	OrgID  string
	Name   string
	Hash   string
	Prefix string

	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// NewAPIKey mints a credential, returning the record to persist and the
// plaintext to hand back to the caller once.
func NewAPIKey(orgID, name string, now time.Time) (*APIKey, string, error) {
	if orgID == "" {
		return nil, "", fmt.Errorf("api key: org id is required")
	}

	raw := make([]byte, apiKeyBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", fmt.Errorf("api key: generating secret: %w", err)
	}
	plaintext := APIKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)

	return &APIKey{
		OrgID:     orgID,
		Name:      name,
		Hash:      HashAPIKey(plaintext),
		Prefix:    displayPrefix(plaintext),
		CreatedAt: now.UTC(),
	}, plaintext, nil
}

// HashAPIKey derives the stored lookup value for a credential.
//
// A plain SHA-256 is the right tool here, unlike for user passwords: the key
// carries 256 bits of entropy from a CSPRNG, so there is no dictionary to
// stretch against, and lookups happen on every request.
func HashAPIKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// displayPrefix keeps enough of a credential to identify it in a list without
// revealing anything usable.
func displayPrefix(plaintext string) string {
	const shown = len(APIKeyPrefix) + 6
	return plaintext[:min(len(plaintext), shown)]
}

// LooksLikeAPIKey reports whether a presented credential has the expected
// shape. Checked before hashing so obviously malformed input never reaches a
// database lookup.
func LooksLikeAPIKey(v string) bool {
	return strings.HasPrefix(v, APIKeyPrefix) && len(v) > len(APIKeyPrefix)+16
}

// Active reports whether the credential may still authenticate.
func (k *APIKey) Active() bool { return k != nil && k.RevokedAt == nil }

// Revoke withdraws the credential.
func (k *APIKey) Revoke(at time.Time) {
	t := at.UTC()
	k.RevokedAt = &t
}
