-- Attested capture: enrolled devices, single-use challenges, and the capture
-- provenance columns on certificates.
--
-- org_id is stored without a foreign key here because the orgs table arrives in
-- 006; the constraint is added there. Migrations are applied as a set, so the
-- gap only exists mid-run.

CREATE TABLE IF NOT EXISTS devices (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id            UUID        NOT NULL,
    platform          TEXT        NOT NULL,
    -- DER-encoded PKIX SubjectPublicKeyInfo of the hardware-backed capture key.
    -- The private half never leaves the device's secure element.
    public_key        BYTEA       NOT NULL,
    attestation_level TEXT        NOT NULL,
    model             TEXT        NOT NULL DEFAULT '',
    status            TEXT        NOT NULL DEFAULT 'active',
    revoked_at        TIMESTAMPTZ,
    revocation_reason TEXT        NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT devices_platform_valid  CHECK (platform IN ('android', 'ios')),
    CONSTRAINT devices_status_valid    CHECK (status IN ('active', 'revoked')),
    CONSTRAINT devices_level_valid     CHECK (attestation_level IN ('software', 'tee', 'strongbox'))
);

CREATE INDEX IF NOT EXISTS idx_devices_org ON devices(org_id);

-- Single-use capture challenges. A nonce is what turns "signed by this device"
-- into "signed by this device, just now, for us" — without it a captured
-- signature could be replayed indefinitely.
CREATE TABLE IF NOT EXISTS capture_nonces (
    value       TEXT PRIMARY KEY,
    org_id      UUID        NOT NULL,
    issued_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ
);

-- Supports the expiry sweep without scanning the table.
CREATE INDEX IF NOT EXISTS idx_capture_nonces_expires ON capture_nonces(expires_at);

ALTER TABLE certificates
    ADD COLUMN IF NOT EXISTS org_id      UUID,
    ADD COLUMN IF NOT EXISTS device_id   UUID REFERENCES devices(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS captured_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_certificates_org    ON certificates(org_id);
CREATE INDEX IF NOT EXISTS idx_certificates_device ON certificates(device_id);

-- Revoking a device must never cascade into its certificates: they are the
-- record of what a now-distrusted device produced, which is exactly what an
-- investigation needs. ON DELETE SET NULL above applies only to hard deletes of
-- the device row, which the application never performs — revocation is a status
-- change.
