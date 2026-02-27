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
		INSERT INTO certificates (content_hash, registrant, tx_hash, block_number, created_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`

	err := r.db.QueryRowContext(ctx, q,
		cert.ContentHash,
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
		SELECT id, content_hash, registrant, tx_hash, block_number, created_at
		FROM certificates
		WHERE content_hash = $1`

	cert := &domain.Certificate{}
	err := r.db.QueryRowContext(ctx, q, contentHash).Scan(
		&cert.ID,
		&cert.ContentHash,
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
	return cert, nil
}
