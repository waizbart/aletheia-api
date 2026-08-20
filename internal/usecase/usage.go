package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
)

// UsageUseCase enforces plan allowances and records billable operations.
//
// Check and Record are separate on purpose: an operation is counted only once
// it has succeeded, so a customer is never billed for a capture the server
// rejected.
type UsageUseCase struct {
	usage UsageRepository
	now   func() time.Time
}

func NewUsageUseCase(usage UsageRepository, now func() time.Time) *UsageUseCase {
	if now == nil {
		now = time.Now
	}
	return &UsageUseCase{usage: usage, now: now}
}

// Check reports whether the org may perform another op this billing period.
func (uc *UsageUseCase) Check(ctx context.Context, org *domain.Org, op domain.Operation) error {
	limit := domain.QuotaFor(org.Plan, op)
	if limit == domain.Unlimited {
		return nil
	}

	used, err := uc.usage.CountForPeriod(ctx, org.ID, op, uc.now())
	if err != nil {
		return fmt.Errorf("quota check: %w", err)
	}
	if used >= limit {
		return fmt.Errorf("quota check: %w: %s allowance of %d for plan %q is spent",
			domain.ErrQuotaExceeded, op, limit, org.Plan)
	}
	return nil
}

// Record counts a completed operation.
func (uc *UsageUseCase) Record(ctx context.Context, orgID string, op domain.Operation) error {
	if err := uc.usage.Record(ctx, orgID, op, uc.now()); err != nil {
		return fmt.Errorf("record usage: %w", err)
	}
	return nil
}

// UsageReport is what an org sees about its own consumption.
type UsageReport struct {
	Period     string
	Plan       domain.Plan
	Operations []UsageLine
}

// UsageLine is one operation's consumption against its allowance.
type UsageLine struct {
	Operation domain.Operation
	Used      int
	// Limit is domain.Unlimited when the plan does not cap this operation.
	Limit int
}

// Summary builds the org's current-period usage report.
func (uc *UsageUseCase) Summary(ctx context.Context, org *domain.Org) (*UsageReport, error) {
	at := uc.now()
	counts, err := uc.usage.Summary(ctx, org.ID, at)
	if err != nil {
		return nil, fmt.Errorf("usage summary: %w", err)
	}

	report := &UsageReport{Period: domain.BillingPeriod(at), Plan: org.Plan}
	// Iterate a fixed list rather than the map so the report is stable and
	// includes operations with no usage yet.
	for _, op := range []domain.Operation{domain.OpAttestedCapture, domain.OpVerify} {
		report.Operations = append(report.Operations, UsageLine{
			Operation: op,
			Used:      counts[op],
			Limit:     domain.QuotaFor(org.Plan, op),
		})
	}
	return report, nil
}
