package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
)

// CreateOrgUseCase onboards a tenant.
type CreateOrgUseCase struct {
	orgs OrgRepository
	now  func() time.Time
}

func NewCreateOrgUseCase(orgs OrgRepository, now func() time.Time) *CreateOrgUseCase {
	if now == nil {
		now = time.Now
	}
	return &CreateOrgUseCase{orgs: orgs, now: now}
}

type CreateOrgInput struct {
	Name string
	Plan domain.Plan
}

func (uc *CreateOrgUseCase) Execute(ctx context.Context, in CreateOrgInput) (*domain.Org, error) {
	if in.Name == "" {
		return nil, fmt.Errorf("create org: name is required")
	}
	if in.Plan == "" {
		in.Plan = domain.PlanDeveloper
	}
	if !domain.ValidPlan(in.Plan) {
		return nil, fmt.Errorf("create org: unknown plan %q", in.Plan)
	}

	org := &domain.Org{
		Name:      in.Name,
		Plan:      in.Plan,
		Status:    domain.OrgActive,
		CreatedAt: uc.now().UTC(),
	}
	if err := uc.orgs.SaveOrg(ctx, org); err != nil {
		return nil, fmt.Errorf("create org: %w", err)
	}
	return org, nil
}

// IssueAPIKeyUseCase mints a credential for an org.
type IssueAPIKeyUseCase struct {
	orgs OrgRepository
	now  func() time.Time
}

func NewIssueAPIKeyUseCase(orgs OrgRepository, now func() time.Time) *IssueAPIKeyUseCase {
	if now == nil {
		now = time.Now
	}
	return &IssueAPIKeyUseCase{orgs: orgs, now: now}
}

type IssueAPIKeyOutput struct {
	Key *domain.APIKey
	// Plaintext is returned exactly once, here. It is never stored and cannot
	// be recovered later — only reissued.
	Plaintext string
}

func (uc *IssueAPIKeyUseCase) Execute(ctx context.Context, orgID, name string) (*IssueAPIKeyOutput, error) {
	org, err := uc.orgs.FindOrgByID(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("issue api key: %w", err)
	}
	if org == nil {
		return nil, fmt.Errorf("issue api key: %w", domain.ErrNotFound)
	}

	key, plaintext, err := domain.NewAPIKey(orgID, name, uc.now())
	if err != nil {
		return nil, fmt.Errorf("issue api key: %w", err)
	}
	if err := uc.orgs.SaveAPIKey(ctx, key); err != nil {
		return nil, fmt.Errorf("issue api key: %w", err)
	}
	return &IssueAPIKeyOutput{Key: key, Plaintext: plaintext}, nil
}

// RevokeAPIKeyUseCase withdraws a credential.
type RevokeAPIKeyUseCase struct {
	orgs OrgRepository
	now  func() time.Time
}

func NewRevokeAPIKeyUseCase(orgs OrgRepository, now func() time.Time) *RevokeAPIKeyUseCase {
	if now == nil {
		now = time.Now
	}
	return &RevokeAPIKeyUseCase{orgs: orgs, now: now}
}

func (uc *RevokeAPIKeyUseCase) Execute(ctx context.Context, keyID string) error {
	if keyID == "" {
		return fmt.Errorf("revoke api key: id is required")
	}
	if err := uc.orgs.RevokeAPIKey(ctx, keyID, uc.now()); err != nil {
		return fmt.Errorf("revoke api key: %w", err)
	}
	return nil
}

// AuthenticateUseCase resolves a presented credential to its owner.
type AuthenticateUseCase struct {
	orgs OrgRepository
}

func NewAuthenticateUseCase(orgs OrgRepository) *AuthenticateUseCase {
	return &AuthenticateUseCase{orgs: orgs}
}

// Execute maps a plaintext credential to an active org.
//
// Every failure — malformed, unknown, revoked, suspended — returns the same
// ErrUnauthorized, so the response cannot be used to distinguish "no such key"
// from "key exists but is revoked".
func (uc *AuthenticateUseCase) Execute(ctx context.Context, plaintext string) (*domain.Org, error) {
	if !domain.LooksLikeAPIKey(plaintext) {
		return nil, domain.ErrUnauthorized
	}

	org, key, err := uc.orgs.FindOrgByAPIKeyHash(ctx, domain.HashAPIKey(plaintext))
	if err != nil {
		return nil, fmt.Errorf("authenticate: %w", err)
	}
	if org == nil || !key.Active() || !org.Active() {
		return nil, domain.ErrUnauthorized
	}
	return org, nil
}
