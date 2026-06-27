package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/lib/pq"

	"github.com/waizbart/aletheia-api/internal/domain"
)

type PostgresCertificateRepo struct {
	db *sql.DB
}

func NewPostgresCertificateRepo(db *sql.DB) *PostgresCertificateRepo {
	return &PostgresCertificateRepo{db: db}
}

func (r *PostgresCertificateRepo) Save(ctx context.Context, cert *domain.Certificate) error {
	const insertCert = `
		INSERT INTO certificates (
			content_hash, phash, phash_bits, orb_descriptors, orb_keypoints, image_blob_key,
			feature_commitment, registrant, tx_hash, block_number, anchor_status, created_at
		)
		VALUES ($1, $2, $3::bit(256), $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id`

	var phash, orbDesc, orbKp, commitment []byte
	if cert.PHash != nil {
		phash = cert.PHash[:]
	}
	if cert.Signature != nil {
		orbDesc = cert.Signature.Descriptors
		orbKp = cert.Signature.Keypoints
	}
	if cert.FeatureCommitment != nil {
		commitment = cert.FeatureCommitment[:]
	}

	var phashBits sql.NullString
	if cert.PHash != nil {
		phashBits = sql.NullString{String: phashToBitString(*cert.PHash), Valid: true}
	}

	var blobKey sql.NullString
	if cert.ImageBlobKey != "" {
		blobKey = sql.NullString{String: cert.ImageBlobKey, Valid: true}
	}

	status := cert.AnchorStatus
	if status == "" {
		status = domain.AnchorAnchored
	}

	if err := r.db.QueryRowContext(ctx, insertCert,
		cert.ContentHash,
		phash,
		phashBits,
		orbDesc,
		orbKp,
		blobKey,
		commitment,
		cert.Registrant,
		cert.TxHash,
		cert.BlockNumber,
		status,
		cert.CreatedAt,
	).Scan(&cert.ID); err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return domain.ErrAlreadyCertified
		}
		return fmt.Errorf("postgres save: %w", err)
	}
	cert.AnchorStatus = status
	return nil
}

// phashToBitString renders a 256-bit pHash as a 256-char '0'/'1' string,
// MSB-first per byte. This matches both domain.Hamming256's bit interpretation
// and migration 004's get_byte()::bit(8) backfill, and is the only portable way
// to bind a bit(256) value through lib/pq (which has no native bit type).
func phashToBitString(p [32]byte) string {
	var sb strings.Builder
	sb.Grow(256)
	for _, b := range p {
		for i := 7; i >= 0; i-- {
			if b&(1<<uint(i)) != 0 {
				sb.WriteByte('1')
			} else {
				sb.WriteByte('0')
			}
		}
	}
	return sb.String()
}

const certificateColumns = `
	id, content_hash, phash, orb_descriptors, orb_keypoints, image_blob_key,
	feature_commitment, registrant, tx_hash, block_number, anchor_status,
	anchor_attempts, anchored_at, created_at`

func scanCertificate(scanner interface {
	Scan(dest ...any) error
}) (*domain.Certificate, error) {
	cert := &domain.Certificate{}
	var phash, orbDesc, orbKp, commitment []byte
	var blobKey sql.NullString
	var anchoredAt sql.NullTime
	if err := scanner.Scan(
		&cert.ID,
		&cert.ContentHash,
		&phash,
		&orbDesc,
		&orbKp,
		&blobKey,
		&commitment,
		&cert.Registrant,
		&cert.TxHash,
		&cert.BlockNumber,
		&cert.AnchorStatus,
		&cert.AnchorAttempts,
		&anchoredAt,
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
	if len(commitment) == 32 {
		var arr [32]byte
		copy(arr[:], commitment)
		cert.FeatureCommitment = &arr
	}
	if blobKey.Valid {
		cert.ImageBlobKey = blobKey.String
	}
	if anchoredAt.Valid {
		t := anchoredAt.Time
		cert.AnchoredAt = &t
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

// Delete removes the certificate matching contentHash. Returns domain.ErrNotFound
// when no row matches.
func (r *PostgresCertificateRepo) Delete(ctx context.Context, contentHash string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM certificates WHERE content_hash = $1`, contentHash)
	if err != nil {
		return fmt.Errorf("postgres delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres delete rows affected: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// FindCandidatesByPHashes resolves the perceptual pre-filter using the pgvector
// bit(256) HNSW index: each rotation variant runs a Hamming KNN against
// phash_bits; the union of returned ids is then re-checked with the exact 256-bit
// Hamming distance in Go. Top-K are returned in distance order. The signature is
// unchanged from the previous LSH implementation so callers and mocks are
// untouched.
func (r *PostgresCertificateRepo) FindCandidatesByPHashes(ctx context.Context, phashes [][32]byte, maxDistance, topK int) ([]*domain.Certificate, error) {
	if len(phashes) == 0 {
		return nil, nil
	}

	candIDs, err := r.candidateIDsByVector(ctx, phashes, maxDistance, topK)
	if err != nil {
		return nil, err
	}
	if len(candIDs) == 0 {
		return nil, nil
	}

	q := `SELECT ` + certificateColumns + ` FROM certificates WHERE id = ANY($1::uuid[]) AND phash IS NOT NULL`
	rows, err := r.db.QueryContext(ctx, q, pq.Array(candIDs))
	if err != nil {
		return nil, fmt.Errorf("postgres fetch candidates: %w", err)
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
			return nil, fmt.Errorf("postgres fetch candidates scan: %w", err)
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
		return nil, fmt.Errorf("postgres fetch candidates rows: %w", err)
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

// candidateIDsByVector runs one Hamming KNN per rotation variant against the
// pgvector HNSW index and unions the resulting ids. ef_search is raised above its
// default (40) because topK can exceed it. The four probes run inside a single
// read transaction so SET LOCAL scopes ef_search to just these queries.
func (r *PostgresCertificateRepo) candidateIDsByVector(ctx context.Context, phashes [][32]byte, maxDistance, topK int) ([]string, error) {
	limit := topK
	if limit <= 0 {
		limit = 64
	}
	efSearch := limit + 40

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("postgres vector probe begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL hnsw.ef_search = %d", efSearch)); err != nil {
		return nil, fmt.Errorf("postgres set ef_search: %w", err)
	}

	const probe = `
		SELECT id::text
		FROM certificates
		WHERE phash_bits IS NOT NULL
		  AND (phash_bits <~> $1::bit(256)) <= $2
		ORDER BY phash_bits <~> $1::bit(256)
		LIMIT $3`

	seen := make(map[string]struct{})
	for _, ph := range phashes {
		bitStr := phashToBitString(ph)
		rows, err := tx.QueryContext(ctx, probe, bitStr, maxDistance, limit)
		if err != nil {
			return nil, fmt.Errorf("postgres vector probe: %w", err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, fmt.Errorf("postgres vector probe scan: %w", err)
			}
			seen[id] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("postgres vector probe rows: %w", err)
		}
		rows.Close()
	}

	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return ids, nil
}

// FindPendingAnchors atomically claims a batch of certificates awaiting on-chain
// anchoring: it flips their status to 'anchoring' and returns them. FOR UPDATE
// SKIP LOCKED makes the claim safe even with multiple workers, and the status
// flip prevents another poll from re-selecting the same rows while the (slow) RPC
// is in flight. Rows that already exhausted maxAttempts are skipped.
func (r *PostgresCertificateRepo) FindPendingAnchors(ctx context.Context, limit, maxAttempts int) ([]*domain.Certificate, error) {
	q := `
		UPDATE certificates
		SET anchor_status = 'anchoring'
		WHERE id IN (
			SELECT id FROM certificates
			WHERE anchor_status = 'pending' AND anchor_attempts < $2
			ORDER BY created_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING ` + certificateColumns

	rows, err := r.db.QueryContext(ctx, q, limit, maxAttempts)
	if err != nil {
		return nil, fmt.Errorf("postgres find pending anchors: %w", err)
	}
	defer rows.Close()

	var out []*domain.Certificate
	for rows.Next() {
		cert, err := scanCertificate(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres find pending anchors scan: %w", err)
		}
		out = append(out, cert)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres find pending anchors rows: %w", err)
	}
	return out, nil
}

// MarkAnchored records a successful on-chain anchor.
func (r *PostgresCertificateRepo) MarkAnchored(ctx context.Context, id, txHash string, blockNum uint64) error {
	const q = `
		UPDATE certificates
		SET anchor_status = 'anchored', tx_hash = $2, block_number = $3,
		    anchored_at = NOW(), anchor_error = NULL
		WHERE id = $1`
	if _, err := r.db.ExecContext(ctx, q, id, txHash, blockNum); err != nil {
		return fmt.Errorf("postgres mark anchored: %w", err)
	}
	return nil
}

// MarkAnchorFailed records a failed anchor attempt. The row returns to 'pending'
// for retry until anchor_attempts reaches maxAttempts, after which it becomes
// 'failed' and is no longer polled.
func (r *PostgresCertificateRepo) MarkAnchorFailed(ctx context.Context, id, errMsg string, maxAttempts int) error {
	const q = `
		UPDATE certificates
		SET anchor_attempts = anchor_attempts + 1,
		    anchor_error = $2,
		    anchor_status = CASE WHEN anchor_attempts + 1 >= $3 THEN 'failed' ELSE 'pending' END
		WHERE id = $1`
	if _, err := r.db.ExecContext(ctx, q, id, errMsg, maxAttempts); err != nil {
		return fmt.Errorf("postgres mark anchor failed: %w", err)
	}
	return nil
}
