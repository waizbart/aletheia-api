-- Multi-tenancy: organisations, API credentials and metered usage.
--
-- Everything the API bills for hangs off an org. This migration also closes the
-- foreign keys that 005 deliberately left open, since devices and certificates
-- were written before orgs existed.

CREATE TABLE IF NOT EXISTS orgs (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT        NOT NULL,
    plan       TEXT        NOT NULL DEFAULT 'developer',
    status     TEXT        NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT orgs_plan_valid   CHECK (plan IN ('developer', 'growth', 'enterprise')),
    CONSTRAINT orgs_status_valid CHECK (status IN ('active', 'suspended'))
);

-- Only the hash of a credential is stored, so a database dump yields nothing
-- usable. key_prefix keeps the first few characters for display in a key list.
CREATE TABLE IF NOT EXISTS api_keys (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       UUID        NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name         TEXT        NOT NULL DEFAULT '',
    key_hash     TEXT        NOT NULL UNIQUE,
    key_prefix   TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);

-- Authentication looks up by hash on every request, so it must be an index
-- probe. The UNIQUE constraint above provides it.
CREATE INDEX IF NOT EXISTS idx_api_keys_org ON api_keys(org_id);

-- One row per (org, operation, billing period). Incremented with an upsert so
-- concurrent operations cannot lose a count and under-bill.
CREATE TABLE IF NOT EXISTS usage_counters (
    org_id    UUID   NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    operation TEXT   NOT NULL,
    period    TEXT   NOT NULL,  -- calendar month, UTC, as YYYY-MM
    count     BIGINT NOT NULL DEFAULT 0,

    PRIMARY KEY (org_id, operation, period)
);

-- Close the foreign keys 005 left open now that orgs exists.
ALTER TABLE devices
    DROP CONSTRAINT IF EXISTS devices_org_fk;
ALTER TABLE devices
    ADD CONSTRAINT devices_org_fk FOREIGN KEY (org_id) REFERENCES orgs(id) ON DELETE CASCADE;

ALTER TABLE certificates
    DROP CONSTRAINT IF EXISTS certificates_org_fk;
ALTER TABLE certificates
    ADD CONSTRAINT certificates_org_fk FOREIGN KEY (org_id) REFERENCES orgs(id) ON DELETE SET NULL;

-- Deleting an org must not erase its certificates: a certificate is a public
-- claim that something was certified at a point in time, and verification of
-- previously issued certificates has to keep working after a tenant leaves.
