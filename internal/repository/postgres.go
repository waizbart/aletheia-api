package repository

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"

	"github.com/waizbart/aletheia-api/internal/domain"
)

type PostgresCertificateRepo struct {
	db *sql.DB
}

func NewPostgresCertificateRepo(db *sql.DB) *PostgresCertificateRepo {
	return &PostgresCertificateRepo{db: db}
}

func (r *PostgresCertificateRepo) Save(ctx context.Context, cert *domain.Certificate) error {
	const q = `
		INSERT INTO certificates (content_hash, perceptual_hash, registrant, tx_hash, block_number, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`

	var perceptualHash sql.NullInt64
	if cert.PerceptualHash != nil {
		perceptualHash = sql.NullInt64{Int64: int64(*cert.PerceptualHash), Valid: true}
	}

	err := r.db.QueryRowContext(ctx, q,
		cert.ContentHash,
		perceptualHash,
		cert.Registrant,
		cert.TxHash,
		cert.BlockNumber,
		cert.CreatedAt,
	).Scan(&cert.ID)

	if err != nil {
		return fmt.Errorf("postgres save: %w", err)
	}
	return nil
}

func (r *PostgresCertificateRepo) FindByHash(ctx context.Context, contentHash string) (*domain.Certificate, error) {
	const q = `
		SELECT id, content_hash, perceptual_hash, registrant, tx_hash, block_number, created_at
		FROM certificates
		WHERE content_hash = $1`

	cert := &domain.Certificate{}
	var perceptualHash sql.NullInt64
	err := r.db.QueryRowContext(ctx, q, contentHash).Scan(
		&cert.ID,
		&cert.ContentHash,
		&perceptualHash,
		&cert.Registrant,
		&cert.TxHash,
		&cert.BlockNumber,
		&cert.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres find by hash: %w", err)
	}
	if perceptualHash.Valid {
		v := uint64(perceptualHash.Int64)
		cert.PerceptualHash = &v
	}
	return cert, nil
}

func (r *PostgresCertificateRepo) FindByPerceptualHash(ctx context.Context, hash uint64, maxDistance int) (*domain.Certificate, error) {
	const q = `
		SELECT id, content_hash, perceptual_hash, registrant, tx_hash, block_number, created_at
		FROM certificates
		WHERE perceptual_hash IS NOT NULL`

	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("postgres find by perceptual hash: %w", err)
	}
	defer rows.Close()

	var (
		best     *domain.Certificate
		bestDist = maxDistance + 1
	)

	for rows.Next() {
		cert := &domain.Certificate{}
		var pHash sql.NullInt64
		if err := rows.Scan(&cert.ID, &cert.ContentHash, &pHash, &cert.Registrant, &cert.TxHash, &cert.BlockNumber, &cert.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres find by perceptual hash scan: %w", err)
		}
		if !pHash.Valid {
			continue
		}
		v := uint64(pHash.Int64)
		cert.PerceptualHash = &v
		d := domain.HammingDistance(hash, v)
		if d <= maxDistance && d < bestDist {
			bestDist = d
			best = cert
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres find by perceptual hash rows: %w", err)
	}

	return best, nil
}
