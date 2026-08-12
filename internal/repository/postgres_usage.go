package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
)

// PostgresUsageRepo counts billable operations per org and billing period.
type PostgresUsageRepo struct {
	db *sql.DB
}

func NewPostgresUsageRepo(db *sql.DB) *PostgresUsageRepo {
	return &PostgresUsageRepo{db: db}
}

// Record increments the period counter.
//
// The upsert keeps the increment atomic under concurrency: two captures
// finishing at once both count, where a read-modify-write would lose one and
// under-bill.
func (r *PostgresUsageRepo) Record(ctx context.Context, orgID string, op domain.Operation, at time.Time) error {
	const q = `
		INSERT INTO usage_counters (org_id, operation, period, count)
		VALUES ($1, $2, $3, 1)
		ON CONFLICT (org_id, operation, period)
		DO UPDATE SET count = usage_counters.count + 1`

	if _, err := r.db.ExecContext(ctx, q, orgID, op, domain.BillingPeriod(at)); err != nil {
		return fmt.Errorf("postgres record usage: %w", err)
	}
	return nil
}

func (r *PostgresUsageRepo) CountForPeriod(ctx context.Context, orgID string, op domain.Operation, at time.Time) (int, error) {
	const q = `
		SELECT count FROM usage_counters
		 WHERE org_id = $1 AND operation = $2 AND period = $3`

	var n int
	err := r.db.QueryRowContext(ctx, q, orgID, op, domain.BillingPeriod(at)).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("postgres count usage: %w", err)
	}
	return n, nil
}

func (r *PostgresUsageRepo) Summary(ctx context.Context, orgID string, at time.Time) (map[domain.Operation]int, error) {
	const q = `
		SELECT operation, count FROM usage_counters
		 WHERE org_id = $1 AND period = $2`

	rows, err := r.db.QueryContext(ctx, q, orgID, domain.BillingPeriod(at))
	if err != nil {
		return nil, fmt.Errorf("postgres usage summary: %w", err)
	}
	defer rows.Close()

	out := make(map[domain.Operation]int)
	for rows.Next() {
		var op domain.Operation
		var n int
		if err := rows.Scan(&op, &n); err != nil {
			return nil, fmt.Errorf("postgres usage summary scan: %w", err)
		}
		out[op] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres usage summary rows: %w", err)
	}
	return out, nil
}
