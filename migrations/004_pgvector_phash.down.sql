DROP INDEX IF EXISTS idx_certificates_phash_hnsw;

ALTER TABLE certificates DROP COLUMN IF EXISTS phash_bits;

-- Recreate the LSH prefilter table dropped by the up migration. Rows are NOT
-- backfilled (the application repopulates them on the next Save); this restores
-- the schema shape only.
CREATE TABLE IF NOT EXISTS phash_bands (
    cert_id    UUID     NOT NULL REFERENCES certificates(id) ON DELETE CASCADE,
    band_idx   SMALLINT NOT NULL,
    band_value SMALLINT NOT NULL,
    PRIMARY KEY (cert_id, band_idx)
);

CREATE INDEX IF NOT EXISTS idx_phash_bands_lookup
    ON phash_bands(band_idx, band_value);

-- Keep the vector extension installed; dropping it could break other objects.
