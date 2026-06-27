-- Transactional-outbox columns for asynchronous on-chain anchoring. The
-- certificate row is inserted with anchor_status='pending' (tx_hash='',
-- block_number=0) and a background worker anchors it, then flips the status to
-- 'anchored'. Status values: pending | anchoring | anchored | failed.
--
-- DEFAULT 'anchored' keeps any pre-existing (synchronously anchored) rows
-- semantically correct after the migration.

ALTER TABLE certificates
    ADD COLUMN IF NOT EXISTS anchor_status   TEXT        NOT NULL DEFAULT 'anchored',
    ADD COLUMN IF NOT EXISTS anchor_attempts INT         NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS anchor_error    TEXT,
    ADD COLUMN IF NOT EXISTS anchored_at     TIMESTAMPTZ;

-- Partial index for the worker poll over the (usually small) pending backlog.
CREATE INDEX IF NOT EXISTS idx_certificates_anchor_pending
    ON certificates (created_at)
    WHERE anchor_status = 'pending';
