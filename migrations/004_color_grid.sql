-- Color grid replaces the S3/MinIO reference image blob entirely.
-- color_grid holds the per-cell mean LAB color of the normalized reference
-- (GridSize² cells × 3 bytes, see domain.ColorGridBytes); ref_width/ref_height
-- are the reference dimensions in the resized keypoint space (≤ 1024, fits
-- SMALLINT). The color-residual matcher reads only these values, so no image
-- is stored anywhere. Legacy rows keep color_grid NULL and are skipped by the
-- visual-similarity path (exact SHA-256 verification still works); re-certify
-- to restore similarity matching for them.

ALTER TABLE certificates
    ADD COLUMN IF NOT EXISTS color_grid BYTEA,
    ADD COLUMN IF NOT EXISTS ref_width  SMALLINT,
    ADD COLUMN IF NOT EXISTS ref_height SMALLINT;

ALTER TABLE certificates DROP COLUMN IF EXISTS image_blob_key;
