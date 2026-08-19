package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/waizbart/aletheia-api/internal/domain"
)

// PostgresDeviceRepo persists enrolled capture devices.
type PostgresDeviceRepo struct {
	db *sql.DB
}

func NewPostgresDeviceRepo(db *sql.DB) *PostgresDeviceRepo {
	return &PostgresDeviceRepo{db: db}
}

const deviceColumns = `
	id, org_id, platform, public_key, attestation_level, model, status,
	revoked_at, revocation_reason, created_at`

func scanDevice(scanner interface{ Scan(dest ...any) error }) (*domain.Device, error) {
	d := &domain.Device{}
	var revokedAt sql.NullTime
	if err := scanner.Scan(
		&d.ID,
		&d.OrgID,
		&d.Platform,
		&d.PublicKey,
		&d.AttestationLevel,
		&d.Model,
		&d.Status,
		&revokedAt,
		&d.RevocationReason,
		&d.CreatedAt,
	); err != nil {
		return nil, err
	}
	if revokedAt.Valid {
		t := revokedAt.Time.UTC()
		d.RevokedAt = &t
	}
	return d, nil
}

func (r *PostgresDeviceRepo) Save(ctx context.Context, d *domain.Device) error {
	const q = `
		INSERT INTO devices (
			org_id, platform, public_key, attestation_level, model, status, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`

	if err := r.db.QueryRowContext(ctx, q,
		d.OrgID, d.Platform, d.PublicKey, d.AttestationLevel, d.Model, d.Status, d.CreatedAt,
	).Scan(&d.ID); err != nil {
		// The use case checks for an existing enrolment first, but two
		// concurrent enrolments of the same key would both pass that check.
		// The unique index is what actually enforces one key per device; this
		// translates it back into the domain error.
		if isUniqueViolation(err, "devices_public_key_key") {
			return domain.ErrDeviceKeyInUse
		}
		return fmt.Errorf("postgres save device: %w", err)
	}
	return nil
}

// FindByPublicKey looks a device up by its attested key.
func (r *PostgresDeviceRepo) FindByPublicKey(ctx context.Context, publicKey []byte) (*domain.Device, error) {
	q := `SELECT ` + deviceColumns + ` FROM devices WHERE public_key = $1`

	d, err := scanDevice(r.db.QueryRowContext(ctx, q, publicKey))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres find device by key: %w", err)
	}
	return d, nil
}

func (r *PostgresDeviceRepo) FindByID(ctx context.Context, id string) (*domain.Device, error) {
	q := `SELECT ` + deviceColumns + ` FROM devices WHERE id = $1`

	d, err := scanDevice(r.db.QueryRowContext(ctx, q, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres find device: %w", err)
	}
	return d, nil
}

func (r *PostgresDeviceRepo) ListByOrg(ctx context.Context, orgID string) ([]*domain.Device, error) {
	q := `SELECT ` + deviceColumns + ` FROM devices WHERE org_id = $1 ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("postgres list devices: %w", err)
	}
	defer rows.Close()

	var out []*domain.Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres list devices scan: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres list devices rows: %w", err)
	}
	return out, nil
}

// Revoke flags the device unusable. Certificates it already produced are left
// exactly as they are.
func (r *PostgresDeviceRepo) Revoke(ctx context.Context, id, reason string, at time.Time) error {
	const q = `
		UPDATE devices
		   SET status = 'revoked', revoked_at = $2, revocation_reason = $3
		 WHERE id = $1`

	res, err := r.db.ExecContext(ctx, q, id, at.UTC(), reason)
	if err != nil {
		return fmt.Errorf("postgres revoke device: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres revoke device rows affected: %w", err)
	}
	if n == 0 {
		return domain.ErrDeviceNotFound
	}
	return nil
}
