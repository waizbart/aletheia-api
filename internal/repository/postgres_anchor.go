package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lib/pq"

	"github.com/waizbart/aletheia-api/internal/domain"
)

// PostgresAnchorRepo backs the batching anchor worker.
type PostgresAnchorRepo struct {
	db *sql.DB
}

func NewPostgresAnchorRepo(db *sql.DB) *PostgresAnchorRepo {
	return &PostgresAnchorRepo{db: db}
}

// PendingLeaves returns unanchored certificates, oldest first, so a backlog
// drains in the order it built up.
func (r *PostgresAnchorRepo) PendingLeaves(ctx context.Context, limit int) ([]*domain.Certificate, error) {
	q := `SELECT ` + certificateColumns + `
		    FROM certificates
		   WHERE anchor_id IS NULL
		ORDER BY created_at
		   LIMIT $1`

	rows, err := r.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres pending leaves: %w", err)
	}
	defer rows.Close()

	var out []*domain.Certificate
	for rows.Next() {
		cert, err := scanCertificate(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres pending leaves scan: %w", err)
		}
		out = append(out, cert)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres pending leaves rows: %w", err)
	}
	return out, nil
}

// SaveUnconfirmedAnchor records a broadcast transaction with no receipt and no
// certificates attached.
//
// The row is deliberately unreferenced: no certificate points at it, so nothing
// advertises an inclusion proof against a root that may never land. It exists
// purely so the transaction hash survives for reconciliation.
func (r *PostgresAnchorRepo) SaveUnconfirmedAnchor(ctx context.Context, a *domain.Anchor) error {
	const q = `
		INSERT INTO anchors (root, leaf_count, tx_hash, block_number, status, created_at)
		VALUES ($1, $2, $3, 0, $4, $5)
		RETURNING id`

	if err := r.db.QueryRowContext(ctx, q,
		a.Root[:], a.LeafCount, a.TxHash, a.Status, a.CreatedAt,
	).Scan(&a.ID); err != nil {
		return fmt.Errorf("postgres save unconfirmed anchor: %w", err)
	}
	return nil
}

// SaveAnchor writes the batch and every certificate's proof in one transaction.
//
// The atomicity matters: a certificate that recorded an anchor id for a row
// that was never inserted would advertise an inclusion proof against a root
// nobody can look up.
func (r *PostgresAnchorRepo) SaveAnchor(ctx context.Context, a *domain.Anchor, leaves []*domain.Certificate) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres save anchor begin: %w", err)
	}
	defer tx.Rollback()

	const insertAnchor = `
		INSERT INTO anchors (root, leaf_count, tx_hash, block_number, status, created_at, confirmed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`

	if err := tx.QueryRowContext(ctx, insertAnchor,
		a.Root[:], a.LeafCount, a.TxHash, a.BlockNumber, a.Status, a.CreatedAt, nullTime(a.ConfirmedAt),
	).Scan(&a.ID); err != nil {
		return fmt.Errorf("postgres save anchor: %w", err)
	}

	const attach = `
		UPDATE certificates
		   SET anchor_id = $2, tx_hash = $3, block_number = $4, leaf_index = $5, merkle_proof = $6
		 WHERE id = $1`

	stmt, err := tx.PrepareContext(ctx, attach)
	if err != nil {
		return fmt.Errorf("postgres attach proof prepare: %w", err)
	}
	defer stmt.Close()

	for _, cert := range leaves {
		if _, err := stmt.ExecContext(ctx,
			cert.ID, a.ID, a.TxHash, a.BlockNumber, cert.LeafIndex, pq.Array(cert.MerkleProof),
		); err != nil {
			return fmt.Errorf("postgres attach proof to %s: %w", cert.ID, err)
		}
		cert.AnchorID = a.ID
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres save anchor commit: %w", err)
	}
	return nil
}
