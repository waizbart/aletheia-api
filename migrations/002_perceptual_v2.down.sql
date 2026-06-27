DROP INDEX IF EXISTS idx_certificates_phash_notnull;

ALTER TABLE certificates
    DROP COLUMN IF EXISTS phash,
    DROP COLUMN IF EXISTS orb_descriptors,
    DROP COLUMN IF EXISTS orb_keypoints,
    DROP COLUMN IF EXISTS image_blob_key;

ALTER TABLE certificates ADD COLUMN IF NOT EXISTS perceptual_hash BIGINT;

CREATE INDEX IF NOT EXISTS idx_certificates_perceptual_hash ON certificates(perceptual_hash);
