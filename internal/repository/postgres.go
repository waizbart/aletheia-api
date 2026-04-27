package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

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
		INSERT INTO certificates (
			content_hash, phash, orb_descriptors, orb_keypoints, image_blob_key,
			registrant, tx_hash, block_number, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id`

	var phash, orbDesc, orbKp []byte
	if cert.PHash != nil {
		phash = cert.PHash[:]
	}
	if cert.Signature != nil {
		orbDesc = cert.Signature.Descriptors
		orbKp = cert.Signature.Keypoints
	}

	var blobKey sql.NullString
	if cert.ImageBlobKey != "" {
		blobKey = sql.NullString{String: cert.ImageBlobKey, Valid: true}
	}

	err := r.db.QueryRowContext(ctx, q,
		cert.ContentHash,
		phash,
		orbDesc,
		orbKp,
		blobKey,
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

const certificateColumns = `
	id, content_hash, phash, orb_descriptors, orb_keypoints, image_blob_key,
	registrant, tx_hash, block_number, created_at`

func scanCertificate(scanner interface {
	Scan(dest ...any) error
}) (*domain.Certificate, error) {
	cert := &domain.Certificate{}
	var phash, orbDesc, orbKp []byte
	var blobKey sql.NullString
	if err := scanner.Scan(
		&cert.ID,
		&cert.ContentHash,
		&phash,
		&orbDesc,
		&orbKp,
		&blobKey,
		&cert.Registrant,
		&cert.TxHash,
		&cert.BlockNumber,
		&cert.CreatedAt,
	); err != nil {
		return nil, err
	}
	if len(phash) == 32 {
		var arr [32]byte
		copy(arr[:], phash)
		cert.PHash = &arr
	}
	if len(orbDesc) > 0 || len(orbKp) > 0 {
		cert.Signature = &domain.FeatureSignature{
			Descriptors: orbDesc,
			Keypoints:   orbKp,
		}
	}
	if blobKey.Valid {
		cert.ImageBlobKey = blobKey.String
	}
	return cert, nil
}

func (r *PostgresCertificateRepo) FindByHash(ctx context.Context, contentHash string) (*domain.Certificate, error) {
	q := `SELECT ` + certificateColumns + ` FROM certificates WHERE content_hash = $1`

	cert, err := scanCertificate(r.db.QueryRowContext(ctx, q, contentHash))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres find by hash: %w", err)
	}
	return cert, nil
}

func (r *PostgresCertificateRepo) FindCandidatesByPHashes(ctx context.Context, phashes [][32]byte, maxDistance, topK int) ([]*domain.Certificate, error) {
	if len(phashes) == 0 {
		return nil, nil
	}

	q := `SELECT ` + certificateColumns + ` FROM certificates WHERE phash IS NOT NULL`

	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("postgres find candidates by phash: %w", err)
	}
	defer rows.Close()

	type scored struct {
		cert *domain.Certificate
		dist int
	}
	var hits []scored

	for rows.Next() {
		cert, err := scanCertificate(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres find candidates by phash scan: %w", err)
		}
		if cert.PHash == nil {
			continue
		}
		minDist := domain.Hamming256(phashes[0], *cert.PHash)
		for _, ph := range phashes[1:] {
			if d := domain.Hamming256(ph, *cert.PHash); d < minDist {
				minDist = d
			}
		}
		if minDist > maxDistance {
			continue
		}
		hits = append(hits, scored{cert: cert, dist: minDist})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres find candidates by phash rows: %w", err)
	}

	sort.Slice(hits, func(i, j int) bool { return hits[i].dist < hits[j].dist })
	if topK > 0 && len(hits) > topK {
		hits = hits[:topK]
	}

	out := make([]*domain.Certificate, len(hits))
	for i, h := range hits {
		out[i] = h.cert
	}
	return out, nil
}
