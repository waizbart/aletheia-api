---
name: dataset-crop-label-contradiction
description: crop_border_20pct and heavy_crop_40pct are the same pixel-identical image but labeled oppositely in the transform taxonomy
metadata:
  type: project
---

In `internal/dataset/transform/registry.go`, `crop_border_20pct` (margin_frac 0.20 per side → keep 0.6 linear → 0.36 area) and `heavy_crop_40pct` (keep_frac 0.60 → 0.36 area) produce the **same pixel-identical image** (verified: identical inliers/mean/max/coverage), yet are labeled `ExpectedMatch=true` vs `false` respectively. No matcher threshold can satisfy both — it is a ground-truth contradiction.

The area-coverage gate (`domain.MinAreaCoverage=0.42`) resolves it the safe way: a crop discarding ~64% of the frame is identity-breaking, so both reject. Coverage is a pure geometric area-ratio (image-independent): crop_border 5/10/15/20% → 0.79/0.64/0.48/0.35. See `tests/feature/coverage_gate_test.go`.

**Why:** the taxonomy author likely read "20% crop" as keep-80% rather than the builder's keep-36%-area semantics.
**How to apply:** when re-labeling/extending the crop families, fix the semantics rather than the thresholds; the matcher cannot distinguish identical images.
