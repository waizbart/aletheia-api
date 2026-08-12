-- Batched Merkle anchoring.
--
-- Certification used to send one transaction per certificate, inline in the
-- request. Anchoring now happens in a background worker that commits a batch
-- under a single Merkle root, and every certificate keeps an inclusion proof so
-- a verifier can check it against the on-chain root without trusting this API.

CREATE TABLE IF NOT EXISTS anchors (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    root         BYTEA       NOT NULL,
    leaf_count   INTEGER     NOT NULL,
    tx_hash      TEXT        NOT NULL,
    block_number BIGINT      NOT NULL DEFAULT 0,
    status       TEXT        NOT NULL DEFAULT 'pending',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    confirmed_at TIMESTAMPTZ,

    CONSTRAINT anchors_status_valid CHECK (status IN ('pending', 'confirmed', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_anchors_tx ON anchors(tx_hash);

ALTER TABLE certificates
    ADD COLUMN IF NOT EXISTS anchor_id    UUID REFERENCES anchors(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS merkle_proof BYTEA[],
    ADD COLUMN IF NOT EXISTS leaf_index   INTEGER;

-- The worker's only query: unanchored certificates, oldest first. A partial
-- index keeps it an index scan over the backlog instead of the whole table,
-- which matters because the backlog is tiny and the table is not.
CREATE INDEX IF NOT EXISTS idx_certificates_pending_anchor
    ON certificates(created_at) WHERE anchor_id IS NULL;

-- tx_hash and block_number stay on certificates as the denormalised answer to
-- "where is this anchored", so verification does not need a join. They are
-- empty and zero until the worker commits the batch.
