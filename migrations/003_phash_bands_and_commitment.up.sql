-- LSH band index over the 256-bit pHash, plus on-chain commitment column.
-- Band layout: 32 bands of 8 bits each (band_idx 0..31 -> phash byte at that index).
-- Lookup: candidate emits 4 rotation variants × 32 bands = 128 (band_idx, band_value)
-- pairs; the composite index turns the previous full-table scan into an index probe.
--
-- NOTE: migration 004 replaces this LSH prefilter with a pgvector bit(256) HNSW
-- index and drops the phash_bands table. It is kept here so the schema history
-- stays linear and reversible.

ALTER TABLE certificates
    ADD COLUMN IF NOT EXISTS feature_commitment BYTEA;

CREATE TABLE IF NOT EXISTS phash_bands (
    cert_id    UUID     NOT NULL REFERENCES certificates(id) ON DELETE CASCADE,
    band_idx   SMALLINT NOT NULL,
    band_value SMALLINT NOT NULL,
    PRIMARY KEY (cert_id, band_idx)
);

CREATE INDEX IF NOT EXISTS idx_phash_bands_lookup
    ON phash_bands(band_idx, band_value);

DROP INDEX IF EXISTS idx_certificates_phash_notnull;
