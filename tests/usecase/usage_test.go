package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/usecase"
)

type mockUsageRepo struct {
	counts    map[domain.Operation]int
	recordFn  func(ctx context.Context, orgID string, op domain.Operation, at time.Time) error
	countFn   func(ctx context.Context, orgID string, op domain.Operation, at time.Time) (int, error)
	summaryFn func(ctx context.Context, orgID string, at time.Time) (map[domain.Operation]int, error)

	recorded []domain.Operation
}

func (m *mockUsageRepo) Record(ctx context.Context, orgID string, op domain.Operation, at time.Time) error {
	m.recorded = append(m.recorded, op)
	if m.recordFn == nil {
		return nil
	}
	return m.recordFn(ctx, orgID, op, at)
}

func (m *mockUsageRepo) CountForPeriod(ctx context.Context, orgID string, op domain.Operation, at time.Time) (int, error) {
	if m.countFn != nil {
		return m.countFn(ctx, orgID, op, at)
	}
	return m.counts[op], nil
}

func (m *mockUsageRepo) Summary(ctx context.Context, orgID string, at time.Time) (map[domain.Operation]int, error) {
	if m.summaryFn != nil {
		return m.summaryFn(ctx, orgID, at)
	}
	return m.counts, nil
}

func developerOrg() *domain.Org {
	return &domain.Org{ID: "org-1", Plan: domain.PlanDeveloper, Status: domain.OrgActive}
}

func TestUsageUseCase_Check(t *testing.T) {
	t.Run("allows inside the allowance", func(t *testing.T) {
		repo := &mockUsageRepo{counts: map[domain.Operation]int{domain.OpAttestedCapture: 499}}
		uc := usecase.NewUsageUseCase(repo, fixedClock())

		if err := uc.Check(context.Background(), developerOrg(), domain.OpAttestedCapture); err != nil {
			t.Fatalf("Check: %v", err)
		}
	})

	t.Run("refuses at the allowance", func(t *testing.T) {
		repo := &mockUsageRepo{counts: map[domain.Operation]int{domain.OpAttestedCapture: 500}}
		uc := usecase.NewUsageUseCase(repo, fixedClock())

		err := uc.Check(context.Background(), developerOrg(), domain.OpAttestedCapture)
		if !errors.Is(err, domain.ErrQuotaExceeded) {
			t.Fatalf("error = %v, want ErrQuotaExceeded", err)
		}
	})

	t.Run("skips the lookup entirely when the plan is uncapped", func(t *testing.T) {
		repo := &mockUsageRepo{countFn: func(context.Context, string, domain.Operation, time.Time) (int, error) {
			t.Fatal("an uncapped plan must not pay for a usage query on every request")
			return 0, nil
		}}
		uc := usecase.NewUsageUseCase(repo, fixedClock())

		org := &domain.Org{ID: "org-1", Plan: domain.PlanGrowth, Status: domain.OrgActive}
		if err := uc.Check(context.Background(), org, domain.OpAttestedCapture); err != nil {
			t.Fatalf("Check: %v", err)
		}
	})

	t.Run("propagates a storage failure", func(t *testing.T) {
		repo := &mockUsageRepo{countFn: func(context.Context, string, domain.Operation, time.Time) (int, error) {
			return 0, errors.New("db down")
		}}
		uc := usecase.NewUsageUseCase(repo, fixedClock())

		err := uc.Check(context.Background(), developerOrg(), domain.OpAttestedCapture)
		if err == nil {
			t.Fatal("expected an error")
		}
		if errors.Is(err, domain.ErrQuotaExceeded) {
			t.Error("a failed lookup must not be reported as an exhausted quota")
		}
	})
}

func TestUsageUseCase_Record(t *testing.T) {
	repo := &mockUsageRepo{}
	uc := usecase.NewUsageUseCase(repo, nil)

	if err := uc.Record(context.Background(), "org-1", domain.OpAttestedCapture); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if len(repo.recorded) != 1 || repo.recorded[0] != domain.OpAttestedCapture {
		t.Errorf("recorded = %v", repo.recorded)
	}

	failing := &mockUsageRepo{recordFn: func(context.Context, string, domain.Operation, time.Time) error {
		return errors.New("db down")
	}}
	if err := usecase.NewUsageUseCase(failing, fixedClock()).Record(context.Background(), "org-1", domain.OpVerify); err == nil {
		t.Error("expected the failure to surface")
	}
}

func TestUsageUseCase_Summary(t *testing.T) {
	repo := &mockUsageRepo{counts: map[domain.Operation]int{domain.OpAttestedCapture: 12}}
	uc := usecase.NewUsageUseCase(repo, fixedClock())

	report, err := uc.Summary(context.Background(), developerOrg())
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}

	if report.Period != "2026-08" {
		t.Errorf("Period = %q, want 2026-08", report.Period)
	}
	if len(report.Operations) != 2 {
		t.Fatalf("got %d operations, want every metered operation listed", len(report.Operations))
	}

	byOp := make(map[domain.Operation]usecase.UsageLine)
	for _, line := range report.Operations {
		byOp[line.Operation] = line
	}

	if got := byOp[domain.OpAttestedCapture]; got.Used != 12 || got.Limit != 500 {
		t.Errorf("capture line = %+v, want used 12 limit 500", got)
	}
	// An operation with no usage yet must still appear, or a customer cannot
	// see what they are entitled to.
	if got := byOp[domain.OpVerify]; got.Used != 0 || got.Limit != 10_000 {
		t.Errorf("verify line = %+v, want used 0 limit 10000", got)
	}
}

func TestUsageUseCase_SummaryFailure(t *testing.T) {
	repo := &mockUsageRepo{summaryFn: func(context.Context, string, time.Time) (map[domain.Operation]int, error) {
		return nil, errors.New("db down")
	}}
	if _, err := usecase.NewUsageUseCase(repo, fixedClock()).Summary(context.Background(), developerOrg()); err == nil {
		t.Fatal("expected the failure to surface")
	}
}
