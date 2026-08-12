package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

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
			content_hash, phash, orb_descriptors, orb_keypoints, color_grid,
			ref_width, ref_height, feature_commitment, registrant, tx_hash,
			block_number, created_at, org_id, device_id, captured_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id`

	var phash, orbDesc, orbKp, colorGrid, commitment []byte
	var refW, refH sql.NullInt32
	if cert.PHash != nil {
		phash = cert.PHash[:]
	}
	if cert.Signature != nil {
		orbDesc = cert.Signature.Descriptors
		orbKp = cert.Signature.Keypoints
		colorGrid = cert.Signature.ColorGrid
		if cert.Signature.RefWidth > 0 {
			refW = sql.NullInt32{Int32: int32(cert.Signature.RefWidth), Valid: true}
		}
		if cert.Signature.RefHeight > 0 {
			refH = sql.NullInt32{Int32: int32(cert.Signature.RefHeight), Valid: true}
		}
	}
	if cert.FeatureCommitment != nil {
		commitment = cert.FeatureCommitment[:]
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres save begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := tx.QueryRowContext(ctx, insertCert,
		cert.ContentHash,
		phash,
		orbDesc,
		orbKp,
		colorGrid,
		refW,
		refH,
		commitment,
		cert.Registrant,
		cert.TxHash,
		cert.BlockNumber,
		cert.CreatedAt,
		nullUUID(cert.OrgID),
		nullUUID(cert.DeviceID),
		nullTime(cert.CapturedAt),
	).Scan(&cert.ID); err != nil {
		return fmt.Errorf("postgres save: %w", err)
	}

	if cert.PHash != nil {
		if err := insertPHashBands(ctx, tx, cert.ID, *cert.PHash); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres save commit: %w", err)
	}
	return nil
}

func insertPHashBands(ctx context.Context, tx *sql.Tx, certID string, phash [32]byte) error {
	bands := domain.PHashBands(phash)
	values := make([]any, 0, 1+2*domain.PHashBandCount)
	values = append(values, certID)
	placeholders := make([]string, 0, domain.PHashBandCount)
	for i := 0; i < domain.PHashBandCount; i++ {
		values = append(values, int16(i), int16(bands[i]))
		placeholders = append(placeholders, fmt.Sprintf("($1, $%d, $%d)", 2+2*i, 3+2*i))
	}
	q := "INSERT INTO phash_bands (cert_id, band_idx, band_value) VALUES " + strings.Join(placeholders, ", ")
	if _, err := tx.ExecContext(ctx, q, values...); err != nil {
		return fmt.Errorf("postgres save phash bands: %w", err)
	}
	return nil
}

const certificateColumns = `
	id, content_hash, phash, orb_descriptors, orb_keypoints, color_grid,
	ref_width, ref_height, feature_commitment, registrant, tx_hash,
	block_number, created_at, org_id, device_id, captured_at,
	anchor_id, leaf_index, merkle_proof`

// nullUUID maps an empty identifier to SQL NULL so the UUID columns stay
// well-typed for rows that have no org or device.
func nullUUID(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func scanCertificate(scanner interface {
	Scan(dest ...any) error
}) (*domain.Certificate, error) {
	cert := &domain.Certificate{}
	var phash, orbDesc, orbKp, colorGrid, commitment []byte
	var refW, refH sql.NullInt32
	var orgID, deviceID, anchorID sql.NullString
	var capturedAt sql.NullTime
	var leafIndex sql.NullInt32
	var merkleProof pq.ByteaArray
	if err := scanner.Scan(
		&cert.ID,
		&cert.ContentHash,
		&phash,
		&orbDesc,
		&orbKp,
		&colorGrid,
		&refW,
		&refH,
		&commitment,
		&cert.Registrant,
		&cert.TxHash,
		&cert.BlockNumber,
		&cert.CreatedAt,
		&orgID,
		&deviceID,
		&capturedAt,
		&anchorID,
		&leafIndex,
		&merkleProof,
	); err != nil {
		return nil, err
	}
	cert.OrgID = orgID.String
	cert.DeviceID = deviceID.String
	cert.AnchorID = anchorID.String
	cert.LeafIndex = int(leafIndex.Int32)
	cert.MerkleProof = merkleProof
	if capturedAt.Valid {
		t := capturedAt.Time.UTC()
		cert.CapturedAt = &t
	}
	if len(phash) == 32 {
		var arr [32]byte
		copy(arr[:], phash)
		cert.PHash = &arr
	}
	if len(orbDesc) > 0 || len(orbKp) > 0 || len(colorGrid) > 0 {
		cert.Signature = &domain.FeatureSignature{
			Descriptors: orbDesc,
			Keypoints:   orbKp,
			ColorGrid:   colorGrid,
			RefWidth:    int(refW.Int32),
			RefHeight:   int(refH.Int32),
		}
	}
	if len(commitment) == 32 {
		var arr [32]byte
		copy(arr[:], commitment)
		cert.FeatureCommitment = &arr
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

// Delete removes the certificate matching contentHash. Associated phash_bands
// rows are removed by the ON DELETE CASCADE foreign key. Returns
// domain.ErrNotFound when no row matches.
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

// FindCandidatesByPHashes resolves the LSH pre-filter: for each rotation
// variant of the candidate, every band byte becomes one (band_idx, band_value)
// probe against phash_bands. The set of cert ids that collide on at least one
// band is then re-checked with the exact 256-bit Hamming distance. Top-K are
// returned in distance order.
func (r *PostgresCertificateRepo) FindCandidatesByPHashes(ctx context.Context, phashes [][32]byte, maxDistance, topK int) ([]*domain.Certificate, error) {
	if len(phashes) == 0 {
		return nil, nil
	}

	candIDs, err := r.candidateIDsByBands(ctx, phashes)
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

// candidateIDsByBands probes the (band_idx, band_value) composite index for
// every band of every candidate rotation. Postgres does not support tuple-IN
// with arrays via lib/pq, so we expand into a flat array of band_idx + a flat
// array of band_value and rely on UNNEST.
func (r *PostgresCertificateRepo) candidateIDsByBands(ctx context.Context, phashes [][32]byte) ([]string, error) {
	seen := make(map[string]struct{})
	totalProbes := len(phashes) * domain.PHashBandCount
	idxArr := make([]int32, 0, totalProbes)
	valArr := make([]int32, 0, totalProbes)
	for _, ph := range phashes {
		bands := domain.PHashBands(ph)
		for i := 0; i < domain.PHashBandCount; i++ {
			idxArr = append(idxArr, int32(i))
			valArr = append(valArr, int32(bands[i]))
		}
	}

	const q = `
		SELECT DISTINCT pb.cert_id::text
		FROM phash_bands pb
		JOIN unnest($1::int[], $2::int[]) AS probe(idx, val)
		  ON pb.band_idx = probe.idx AND pb.band_value = probe.val`

	rows, err := r.db.QueryContext(ctx, q, pq.Array(idxArr), pq.Array(valArr))
	if err != nil {
		return nil, fmt.Errorf("postgres band probe: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres band probe scan: %w", err)
		}
		seen[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres band probe rows: %w", err)
	}

	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return ids, nil
}
