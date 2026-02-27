CREATE TABLE IF NOT EXISTS certificates (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    content_hash TEXT NOT NULL UNIQUE,
    registrant   TEXT NOT NULL,
    tx_hash      TEXT NOT NULL,
    block_number BIGINT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_certificates_content_hash ON certificates(content_hash);
