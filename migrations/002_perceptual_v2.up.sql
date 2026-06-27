DROP INDEX IF EXISTS idx_certificates_perceptual_hash;

ALTER TABLE certificates DROP COLUMN IF EXISTS perceptual_hash;

ALTER TABLE certificates
    ADD COLUMN IF NOT EXISTS phash           BYTEA,
    ADD COLUMN IF NOT EXISTS orb_descriptors BYTEA,
    ADD COLUMN IF NOT EXISTS orb_keypoints   BYTEA,
    ADD COLUMN IF NOT EXISTS image_blob_key  TEXT;

CREATE INDEX IF NOT EXISTS idx_certificates_phash_notnull
    ON certificates(id) WHERE phash IS NOT NULL;
