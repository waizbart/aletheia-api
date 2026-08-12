package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
)

// PostgresOrgRepo persists tenants and their API credentials.
type PostgresOrgRepo struct {
	db *sql.DB
}

func NewPostgresOrgRepo(db *sql.DB) *PostgresOrgRepo {
	return &PostgresOrgRepo{db: db}
}

func (r *PostgresOrgRepo) SaveOrg(ctx context.Context, o *domain.Org) error {
	const q = `
		INSERT INTO orgs (name, plan, status, created_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id`

	if err := r.db.QueryRowContext(ctx, q, o.Name, o.Plan, o.Status, o.CreatedAt).Scan(&o.ID); err != nil {
		return fmt.Errorf("postgres save org: %w", err)
	}
	return nil
}

func (r *PostgresOrgRepo) FindOrgByID(ctx context.Context, id string) (*domain.Org, error) {
	const q = `SELECT id, name, plan, status, created_at FROM orgs WHERE id = $1`

	o := &domain.Org{}
	err := r.db.QueryRowContext(ctx, q, id).Scan(&o.ID, &o.Name, &o.Plan, &o.Status, &o.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres find org: %w", err)
	}
	return o, nil
}

func (r *PostgresOrgRepo) SaveAPIKey(ctx context.Context, k *domain.APIKey) error {
	const q = `
		INSERT INTO api_keys (org_id, name, key_hash, key_prefix, created_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`

	if err := r.db.QueryRowContext(ctx, q, k.OrgID, k.Name, k.Hash, k.Prefix, k.CreatedAt).Scan(&k.ID); err != nil {
		return fmt.Errorf("postgres save api key: %w", err)
	}
	return nil
}

// FindOrgByAPIKeyHash resolves a credential to its owner in a single join, and
// stamps last_used_at so an operator can spot dormant keys.
func (r *PostgresOrgRepo) FindOrgByAPIKeyHash(ctx context.Context, hash string) (*domain.Org, *domain.APIKey, error) {
	const q = `
		SELECT o.id, o.name, o.plan, o.status, o.created_at,
		       k.id, k.org_id, k.name, k.key_hash, k.key_prefix, k.created_at,
		       k.last_used_at, k.revoked_at
		  FROM api_keys k
		  JOIN orgs o ON o.id = k.org_id
		 WHERE k.key_hash = $1`

	o := &domain.Org{}
	k := &domain.APIKey{}
	var lastUsed, revoked sql.NullTime

	err := r.db.QueryRowContext(ctx, q, hash).Scan(
		&o.ID, &o.Name, &o.Plan, &o.Status, &o.CreatedAt,
		&k.ID, &k.OrgID, &k.Name, &k.Hash, &k.Prefix, &k.CreatedAt,
		&lastUsed, &revoked,
	)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("postgres find org by api key: %w", err)
	}
	if lastUsed.Valid {
		t := lastUsed.Time.UTC()
		k.LastUsedAt = &t
	}
	if revoked.Valid {
		t := revoked.Time.UTC()
		k.RevokedAt = &t
	}

	// Best-effort: a failure to stamp usage must not fail the request.
	_, _ = r.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = NOW() WHERE id = $1`, k.ID)

	return o, k, nil
}

func (r *PostgresOrgRepo) RevokeAPIKey(ctx context.Context, id string, at time.Time) error {
	const q = `UPDATE api_keys SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL`

	res, err := r.db.ExecContext(ctx, q, id, at.UTC())
	if err != nil {
		return fmt.Errorf("postgres revoke api key: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres revoke api key rows affected: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
