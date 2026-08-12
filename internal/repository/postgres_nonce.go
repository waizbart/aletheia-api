package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
)

// PostgresNonceRepo persists single-use capture challenges.
type PostgresNonceRepo struct {
	db *sql.DB
}

func NewPostgresNonceRepo(db *sql.DB) *PostgresNonceRepo {
	return &PostgresNonceRepo{db: db}
}

func (r *PostgresNonceRepo) Save(ctx context.Context, n domain.CaptureNonce) error {
	const q = `
		INSERT INTO capture_nonces (value, org_id, issued_at, expires_at)
		VALUES ($1, $2, $3, $4)`

	if _, err := r.db.ExecContext(ctx, q, n.Value, n.OrgID, n.IssuedAt, n.ExpiresAt); err != nil {
		return fmt.Errorf("postgres save nonce: %w", err)
	}
	return nil
}

// Consume spends a challenge atomically.
//
// The guard lives in the UPDATE's WHERE clause rather than in a read-then-write
// pair on purpose: two captures presenting the same nonce concurrently would
// both pass a separate existence check, and both would be certified. Here
// exactly one UPDATE matches and the loser gets no row back.
func (r *PostgresNonceRepo) Consume(ctx context.Context, value string, now time.Time) (*domain.CaptureNonce, error) {
	const q = `
		UPDATE capture_nonces
		   SET consumed_at = $2
		 WHERE value = $1
		   AND consumed_at IS NULL
		   AND expires_at > $2
		RETURNING value, org_id, issued_at, expires_at, consumed_at`

	n := &domain.CaptureNonce{}
	var consumedAt sql.NullTime
	err := r.db.QueryRowContext(ctx, q, value, now.UTC()).Scan(
		&n.Value, &n.OrgID, &n.IssuedAt, &n.ExpiresAt, &consumedAt,
	)
	if err == sql.ErrNoRows {
		// Unknown, already spent, or expired — deliberately indistinguishable.
		return nil, domain.ErrNonceUnusable
	}
	if err != nil {
		return nil, fmt.Errorf("postgres consume nonce: %w", err)
	}
	if consumedAt.Valid {
		t := consumedAt.Time.UTC()
		n.ConsumedAt = &t
	}
	return n, nil
}

// DeleteExpired prunes spent and lapsed challenges.
func (r *PostgresNonceRepo) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	const q = `DELETE FROM capture_nonces WHERE expires_at < $1`

	res, err := r.db.ExecContext(ctx, q, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("postgres prune nonces: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("postgres prune nonces rows affected: %w", err)
	}
	return n, nil
}
