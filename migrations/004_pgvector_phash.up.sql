-- Replace the phash_bands LSH prefilter with a pgvector bit(256) HNSW index.
-- Requires pgvector >= 0.7.0 (bit vectors, bit_hamming_ops, the <~> Hamming
-- operator). Use the pgvector/pgvector:pg16 image; postgres:*-alpine cannot
-- CREATE EXTENSION vector.

CREATE EXTENSION IF NOT EXISTS vector;

ALTER TABLE certificates ADD COLUMN IF NOT EXISTS phash_bits bit(256);

-- Backfill phash_bits from the existing phash BYTEA. Each of the 32 bytes is
-- expanded MSB-first (get_byte -> bit(8)) and concatenated in index order, which
-- reproduces exactly the 256-bit layout used by domain.Hamming256 / PHash256.
UPDATE certificates c
SET phash_bits = sub.bits::bit(256)
FROM (
    SELECT id,
           string_agg(get_byte(phash, g)::bit(8)::text, '' ORDER BY g) AS bits
    FROM certificates, generate_series(0, 31) AS g
    WHERE phash IS NOT NULL
    GROUP BY id
) sub
WHERE c.id = sub.id
  AND c.phash IS NOT NULL;

-- Hamming-distance HNSW index. Partial so non-image certs (NULL phash_bits) are
-- skipped entirely.
CREATE INDEX IF NOT EXISTS idx_certificates_phash_hnsw
    ON certificates USING hnsw (phash_bits bit_hamming_ops)
    WHERE phash_bits IS NOT NULL;

DROP INDEX IF EXISTS idx_phash_bands_lookup;
DROP TABLE IF EXISTS phash_bands;
