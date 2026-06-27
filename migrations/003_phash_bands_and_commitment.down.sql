DROP INDEX IF EXISTS idx_phash_bands_lookup;

DROP TABLE IF EXISTS phash_bands;

ALTER TABLE certificates DROP COLUMN IF EXISTS feature_commitment;

CREATE INDEX IF NOT EXISTS idx_certificates_phash_notnull
    ON certificates(id) WHERE phash IS NOT NULL;
