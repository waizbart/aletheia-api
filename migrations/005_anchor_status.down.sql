DROP INDEX IF EXISTS idx_certificates_anchor_pending;

ALTER TABLE certificates
    DROP COLUMN IF EXISTS anchor_status,
    DROP COLUMN IF EXISTS anchor_attempts,
    DROP COLUMN IF EXISTS anchor_error,
    DROP COLUMN IF EXISTS anchored_at;
