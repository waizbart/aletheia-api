package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

type mockOrgRepo struct {
	saveOrgFn   func(ctx context.Context, o *domain.Org) error
	findOrgFn   func(ctx context.Context, id string) (*domain.Org, error)
	saveKeyFn   func(ctx context.Context, k *domain.APIKey) error
	findByHash  func(ctx context.Context, hash string) (*domain.Org, *domain.APIKey, error)
	revokeKeyFn func(ctx context.Context, id string, at time.Time) error

	savedKey *domain.APIKey
}

func (m *mockOrgRepo) SaveOrg(ctx context.Context, o *domain.Org) error {
	if m.saveOrgFn == nil {
		o.ID = "org-1"
		return nil
	}
	return m.saveOrgFn(ctx, o)
}

func (m *mockOrgRepo) FindOrgByID(ctx context.Context, id string) (*domain.Org, error) {
	if m.findOrgFn == nil {
		return &domain.Org{ID: id, Plan: domain.PlanDeveloper, Status: domain.OrgActive}, nil
	}
	return m.findOrgFn(ctx, id)
}

func (m *mockOrgRepo) SaveAPIKey(ctx context.Context, k *domain.APIKey) error {
	m.savedKey = k
	if m.saveKeyFn == nil {
		k.ID = "key-1"
		return nil
	}
	return m.saveKeyFn(ctx, k)
}

func (m *mockOrgRepo) FindOrgByAPIKeyHash(ctx context.Context, hash string) (*domain.Org, *domain.APIKey, error) {
	if m.findByHash == nil {
		return nil, nil, nil
	}
	return m.findByHash(ctx, hash)
}

func (m *mockOrgRepo) RevokeAPIKey(ctx context.Context, id string, at time.Time) error {
	if m.revokeKeyFn == nil {
		return nil
	}
	return m.revokeKeyFn(ctx, id, at)
}

func TestCreateOrgUseCase(t *testing.T) {
	t.Run("creates an active org", func(t *testing.T) {
		uc := usecase.NewCreateOrgUseCase(&mockOrgRepo{}, fixedClock())

		org, err := uc.Execute(context.Background(), usecase.CreateOrgInput{Name: "Acme", Plan: domain.PlanGrowth})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if org.Status != domain.OrgActive {
			t.Errorf("Status = %q, want active", org.Status)
		}
		if org.Plan != domain.PlanGrowth {
			t.Errorf("Plan = %q, want growth", org.Plan)
		}
	})

	t.Run("defaults to the developer plan", func(t *testing.T) {
		uc := usecase.NewCreateOrgUseCase(&mockOrgRepo{}, nil)

		org, err := uc.Execute(context.Background(), usecase.CreateOrgInput{Name: "Acme"})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if org.Plan != domain.PlanDeveloper {
			t.Errorf("Plan = %q, want developer", org.Plan)
		}
	})

	t.Run("rejects bad input", func(t *testing.T) {
		uc := usecase.NewCreateOrgUseCase(&mockOrgRepo{}, fixedClock())

		if _, err := uc.Execute(context.Background(), usecase.CreateOrgInput{}); err == nil {
			t.Error("a name is required")
		}
		if _, err := uc.Execute(context.Background(), usecase.CreateOrgInput{Name: "Acme", Plan: "platinum"}); err == nil {
			t.Error("an unknown plan should be rejected")
		}
	})

	t.Run("propagates a storage failure", func(t *testing.T) {
		repo := &mockOrgRepo{saveOrgFn: func(context.Context, *domain.Org) error { return errors.New("db down") }}
		uc := usecase.NewCreateOrgUseCase(repo, fixedClock())

		if _, err := uc.Execute(context.Background(), usecase.CreateOrgInput{Name: "Acme"}); err == nil {
			t.Error("expected the failure to surface")
		}
	})
}

func TestIssueAPIKeyUseCase(t *testing.T) {
	t.Run("returns the plaintext once and stores only the hash", func(t *testing.T) {
		repo := &mockOrgRepo{}
		uc := usecase.NewIssueAPIKeyUseCase(repo, fixedClock())

		out, err := uc.Execute(context.Background(), "org-1", "ci")
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if out.Plaintext == "" {
			t.Fatal("the caller must receive the credential")
		}
		if repo.savedKey.Hash == out.Plaintext {
			t.Fatal("the plaintext must never be what is persisted")
		}
		if repo.savedKey.Hash != domain.HashAPIKey(out.Plaintext) {
			t.Error("the stored hash must correspond to the issued credential")
		}
	})

	t.Run("refuses an unknown org", func(t *testing.T) {
		repo := &mockOrgRepo{findOrgFn: func(context.Context, string) (*domain.Org, error) { return nil, nil }}
		uc := usecase.NewIssueAPIKeyUseCase(repo, nil)

		_, err := uc.Execute(context.Background(), "nope", "ci")
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
	})

	t.Run("propagates failures", func(t *testing.T) {
		lookup := &mockOrgRepo{findOrgFn: func(context.Context, string) (*domain.Org, error) {
			return nil, errors.New("db down")
		}}
		if _, err := usecase.NewIssueAPIKeyUseCase(lookup, fixedClock()).Execute(context.Background(), "org-1", ""); err == nil {
			t.Error("expected the lookup failure to surface")
		}

		save := &mockOrgRepo{saveKeyFn: func(context.Context, *domain.APIKey) error { return errors.New("db down") }}
		if _, err := usecase.NewIssueAPIKeyUseCase(save, fixedClock()).Execute(context.Background(), "org-1", ""); err == nil {
			t.Error("expected the save failure to surface")
		}
	})
}

func TestRevokeAPIKeyUseCase(t *testing.T) {
	t.Run("revokes", func(t *testing.T) {
		var gotID string
		repo := &mockOrgRepo{revokeKeyFn: func(_ context.Context, id string, _ time.Time) error {
			gotID = id
			return nil
		}}
		if err := usecase.NewRevokeAPIKeyUseCase(repo, fixedClock()).Execute(context.Background(), "key-1"); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if gotID != "key-1" {
			t.Errorf("revoked %q, want key-1", gotID)
		}
	})

	t.Run("rejects an empty id", func(t *testing.T) {
		if err := usecase.NewRevokeAPIKeyUseCase(&mockOrgRepo{}, nil).Execute(context.Background(), ""); err == nil {
			t.Error("an id is required")
		}
	})

	t.Run("propagates a storage failure", func(t *testing.T) {
		repo := &mockOrgRepo{revokeKeyFn: func(context.Context, string, time.Time) error { return errors.New("db down") }}
		if err := usecase.NewRevokeAPIKeyUseCase(repo, fixedClock()).Execute(context.Background(), "key-1"); err == nil {
			t.Error("expected the failure to surface")
		}
	})
}

// TestAuthenticateUseCase pins the property that matters most here: every
// failure looks identical from the outside, so a caller cannot use the response
// to learn which credentials exist.
func TestAuthenticateUseCase(t *testing.T) {
	_, plaintext, err := domain.NewAPIKey("org-1", "ci", captureNow)
	if err != nil {
		t.Fatal(err)
	}

	activeOrg := &domain.Org{ID: "org-1", Status: domain.OrgActive, Plan: domain.PlanDeveloper}
	activeKey := &domain.APIKey{ID: "key-1", OrgID: "org-1"}
	revokedAt := captureNow
	revokedKey := &domain.APIKey{ID: "key-1", OrgID: "org-1", RevokedAt: &revokedAt}

	tests := []struct {
		name       string
		token      string
		resolve    func(ctx context.Context, hash string) (*domain.Org, *domain.APIKey, error)
		wantOrg    bool
		wantErr    error
		wantAnyErr bool
	}{
		{
			name:  "valid credential resolves its org",
			token: plaintext,
			resolve: func(context.Context, string) (*domain.Org, *domain.APIKey, error) {
				return activeOrg, activeKey, nil
			},
			wantOrg: true,
		},
		{
			name:  "malformed credential never reaches the database",
			token: "not-a-key",
			resolve: func(context.Context, string) (*domain.Org, *domain.APIKey, error) {
				t.Fatal("must not query")
				return nil, nil, nil
			},
			wantErr: domain.ErrUnauthorized,
		},
		{
			name:    "unknown credential",
			token:   plaintext,
			resolve: func(context.Context, string) (*domain.Org, *domain.APIKey, error) { return nil, nil, nil },
			wantErr: domain.ErrUnauthorized,
		},
		{
			name:  "revoked credential",
			token: plaintext,
			resolve: func(context.Context, string) (*domain.Org, *domain.APIKey, error) {
				return activeOrg, revokedKey, nil
			},
			wantErr: domain.ErrUnauthorized,
		},
		{
			name:  "suspended org",
			token: plaintext,
			resolve: func(context.Context, string) (*domain.Org, *domain.APIKey, error) {
				return &domain.Org{ID: "org-1", Status: domain.OrgSuspended}, activeKey, nil
			},
			wantErr: domain.ErrUnauthorized,
		},
		{
			name:  "storage failure is not an authorisation failure",
			token: plaintext,
			resolve: func(context.Context, string) (*domain.Org, *domain.APIKey, error) {
				return nil, nil, errors.New("db down")
			},
			wantAnyErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc := usecase.NewAuthenticateUseCase(&mockOrgRepo{findByHash: tt.resolve})

			org, err := uc.Execute(context.Background(), tt.token)
			switch {
			case tt.wantOrg:
				if err != nil {
					t.Fatalf("Execute: %v", err)
				}
				if org.ID != "org-1" {
					t.Errorf("org = %v", org)
				}
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
			case tt.wantAnyErr:
				if err == nil {
					t.Fatal("expected an error")
				}
				if errors.Is(err, domain.ErrUnauthorized) {
					t.Error("a storage failure must not be reported as unauthorized")
				}
				if !strings.Contains(err.Error(), "db down") {
					t.Errorf("error %q should carry the cause", err)
				}
			}
		})
	}
}

// TestIssueAPIKeyUseCase_RejectsEmptyOrg guards the boundary between the use
// case and the domain: if a repository ever resolved an empty id to an org, the
// credential must still not be minted against it.
func TestIssueAPIKeyUseCase_RejectsEmptyOrg(t *testing.T) {
	repo := &mockOrgRepo{findOrgFn: func(_ context.Context, id string) (*domain.Org, error) {
		return &domain.Org{ID: id, Status: domain.OrgActive}, nil
	}}

	_, err := usecase.NewIssueAPIKeyUseCase(repo, fixedClock()).Execute(context.Background(), "", "ci")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "org id is required") {
		t.Errorf("error %q should name the cause", err)
	}
}
