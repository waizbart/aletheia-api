-- Video certification: whole-file video certificates live in the same table as
-- images, so the anchor worker, the Merkle leaf and the exact-hash lookup are
-- untouched by this feature.
--
-- A video is reduced to one anchor frame plus a sequence of frame pHashes. The
-- anchor frame fills the existing perceptual columns (phash, orb_descriptors,
-- orb_keypoints, color_grid, ref_width/ref_height) and is indexed in
-- phash_bands like any image, which is what lets the image matcher run against
-- video unchanged.
--
-- frame_phashes is a column rather than a table on purpose: it holds at most
-- VideoFrameSamples x 32 bytes (1 KB), it is always read as a unit, and nothing
-- queries an individual frame because excerpt search is deliberately out of
-- scope. A table would buy a join and a cascade for nothing. Should per-frame
-- lookup ever be needed, promoting this to a table is a migration, not a
-- rewrite.
--
-- media_kind partitions the perceptual candidate lookup. It is not cosmetic:
-- video frame pHashes are computed after a box-filter resize that image pHashes
-- do not get, so the two live in different metric spaces and comparing across
-- them yields noise. Without the partition, a still grabbed from a certified
-- video would also "verify" and return a certificate whose content_hash the
-- caller cannot reproduce.

ALTER TABLE certificates
    ADD COLUMN IF NOT EXISTS media_kind    TEXT,
    ADD COLUMN IF NOT EXISTS duration_ms   INTEGER,
    ADD COLUMN IF NOT EXISTS frame_phashes BYTEA,
    ADD COLUMN IF NOT EXISTS anchor_index  SMALLINT;

-- Every row that predates this migration is an image. Backfilling before the
-- NOT NULL lets the constraint be declared without a separate deploy step.
UPDATE certificates SET media_kind = 'image' WHERE media_kind IS NULL;

ALTER TABLE certificates ALTER COLUMN media_kind SET DEFAULT 'image';
ALTER TABLE certificates ALTER COLUMN media_kind SET NOT NULL;

-- There is no ADD CONSTRAINT IF NOT EXISTS, and this file must stay rerunnable:
-- migrations here are applied by filename order with no runner and no version
-- table.
DO $$ BEGIN
    ALTER TABLE certificates ADD CONSTRAINT certificates_media_kind_valid
        CHECK (media_kind IN ('image', 'video'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- The candidate fetch filters by media_kind after the band probe narrows the
-- set, so this index only has to help that second step.
CREATE INDEX IF NOT EXISTS idx_certificates_media_kind ON certificates(media_kind);
