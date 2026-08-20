-- One hardware key, one device record.
--
-- Devices were identified only by their row id, so a revoked device could
-- enrol its same attested key again and receive a fresh active record:
-- revocation applied to a row the device could simply replace.
--
-- The use case now refuses a re-enrolment of a known key, but two concurrent
-- enrolments would both pass that check. This constraint is what actually
-- enforces the invariant.
--
-- 005 declares the same constraint inline for fresh installs. This migration
-- exists for databases that already ran 005, where CREATE TABLE IF NOT EXISTS
-- would skip it. Nothing is deleted or merged here on purpose: a duplicate
-- means two device records already vouched for certificates, and silently
-- deleting one would strip provenance off rows that reference it. If this
-- fails, look at the duplicates before deciding which record is real.
ALTER TABLE devices
    DROP CONSTRAINT IF EXISTS devices_public_key_key;
ALTER TABLE devices
    ADD CONSTRAINT devices_public_key_key UNIQUE (public_key);
