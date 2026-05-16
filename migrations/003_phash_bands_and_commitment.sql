-- LSH band index over the 256-bit pHash, plus on-chain commitment column.
-- Band layout: 32 bands of 8 bits each (band_idx 0..31 -> phash byte at that index).
-- Lookup: candidate emits 4 rotation variants × 32 bands = 128 (band_idx, band_value)
-- pairs; the composite index turns the previous full-table scan into an index probe.

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
