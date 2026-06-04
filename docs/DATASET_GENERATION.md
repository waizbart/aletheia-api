# Test Dataset Generation

This document describes the test-dataset generation pipeline for Aletheia API — how it works,
why it's designed the way it is, and how to reproduce it.

## Why a synthetic dataset?

The verification pipeline (`POST /certificates/verify`) returns `certified: true/false` using a
multi-stage decision:

1. **Exact match** — SHA-256 lookup in the database.
2. **Visual similarity fallback** — only reached when SHA-256 fails:
   - pHash LSH prefilter (Hamming-256 ≤ 96 across 4 rotation variants)
   - ORB homography (≥ 20 RANSAC inliers)
   - LAB color residual (per-image mean ≤ 8.0 AND max single cell ≤ 38.0)

The curated golden set (`testdata/curated/aletheia/`) covers the documented edge cases for
**one specific photo**. That is valuable for regression but not representative of how the
pipeline behaves on diverse real-world imagery. The synthetic dataset scales this to **1000
images × ~40 transform variants** (~40 000 samples), enabling:

- Precision/recall measurement across diverse content.
- Per-transform confusion matrices to find where the boundary actually sits.
- Detection of regressions when thresholds or algorithms change.

## Ground-truth principle

Labels are assigned **a-priori from intent**, not from the algorithm's current output.
This is the key property that makes the dataset a benchmark rather than a tautology.

- Identity-preserving edits (recompression, resize, format change, rotation, small crop,
  light noise/brightness) → `expected_match: true`
- Identity-breaking edits (global color filters, localized recolor, content overlays,
  heavy crops) and different images → `expected_match: false`
- Rungs near the documented boundary are flagged `confidence: "borderline"` and excluded
  from the hard precision/recall gates so they can be studied without noisily failing tests.

The manifest embeds `thresholds_snapshot` (the values of `MinInliers`, `MaxColorMean`,
`MaxCellDist`, `MaxPHashDistance` at generation time) so a future reader knows if labels
predate a threshold change.

## Architecture

```
cmd/datasetgen/                  CLI entry point
internal/dataset/
  source/
    source.go                    Source interface
    picsum.go                    Lorem Picsum adapter (seeded, resumable, no API key)
    local.go                     Offline adapter (uses testdata/curated/smoke-base)
  transform/
    registry.go                  Single source of truth: family → ladder → {expect, confidence}
    builders.go                  gocv builders (reuses patterns from dataset_matrix_test.go)
  manifest/
    manifest.go                  JSON schema structs + writer
    csv.go                       Flat CSV writer
testdata/
  curated/                       Committed: hand-made oracle (28 + 4 + 20 files)
  generated/                     Git-ignored: downloaded + derived output
```

The `internal/testdata` resolver package lets any test or lab tool reference paths without
caring about working directory. `testdata.Curated("aletheia")` always returns the right
absolute path regardless of whether the caller is in `tests/`, `lab/hashing/*/`, or `cmd/`.

## Transform taxonomy

Transforms are defined in `internal/dataset/transform/registry.go` and grouped into families.
The table below is the canonical list; the registry is the code source of truth.

### Identity-preserving (`expected_match: true`)

| Family | Parameter ladder | Confidence | Rationale |
|---|---|---|---|
| `jpeg_recompress` | q90, q70, q50, q30, q20, q10 | q10 = borderline | q10 ≈ 5.8 LAB mean — documented low edge of pass region |
| `downscale` | 0.75×, 0.5×, 0.33×, →256px, →160px | →160px = borderline | Pipeline resizes to max 1024px; ORB holds ≥ 20 inliers above ~128px |
| `upscale` | 1.5×, 2.0× | high | Resampling only, no new content introduced |
| `format_change` | PNG, GIF, BMP, TIFF, WebP | high | Format change confirmed as match in curated oracle |
| `rotate_cardinal` | 90°, 180°, 270° | high | pHash computes 4 rotation variants; homography handles the rest |
| `rotate_small` | 5°, 10°, 32° | 32° = borderline | Repo: 32° passes; larger angles grow border fill and reduce inliers |
| `crop_border` | 5%, 10%, 15%, 20% margin | 20% = borderline | Repo: 10% matches; > ~25% drops inliers below MinInliers=20 |
| `brightness` | ±5%, ±10% | ±10% = borderline | Small global L shift; mean stays < 8.0 until ~10–12% |
| `noise_light` | Gaussian σ=5, σ=10 | high | Low-amplitude; ORB and color residual unaffected |
| `sharpen` | Unsharp light | high | Edge enhancement; negligible color residual |
| `whatsapp_like` | 960px cap, q=40 | high | Mirrors documented positive from existing test suite |
| `p3_as_srgb` | q=70 | high | Display P3 pixel values treated as sRGB; LAB shift < 8.0 |

### Identity-breaking (`expected_match: false`)

| Family | Parameter ladder | Confidence | Rationale |
|---|---|---|---|
| `grayscale` | full | high | Global a/b channel collapse → LAB mean ≫ 8.0 |
| `sepia` | matrix | high | Strong global remap; repo filters produce ~11+ mean |
| `hue_shift` | 30°, 60°, 120°, 180° | 30° = borderline | Rotates a/b; small shifts near the boundary |
| `saturation_boost` | 1.5×, 2.0× | 1.5× = borderline | Existing test: `heavy_saturation_filter` → false |
| `color_invert` | invert | high | Maximal color residual; all cells spike |
| `localized_recolor` | region recolor | high | Spikes one cell above MaxCellDist=38 (repo `aletheia-red-dress`) |
| `content_overlay` | 10%, 15%, 20%, 30% area | 10% = borderline | Per-cell max trips (repo `aletheia-sword`, `rectangle_overlay_15pct`) |
| `heavy_crop` | 40%, 50%, 60% | 40% = borderline | Inliers < MinInliers=20 and framing change |
| `different_image` | peer (cyclic pairing) | high | Primary negative control — different content must never match |

## Lorem Picsum as image source

[Lorem Picsum](https://picsum.photos) provides deterministic, stable photo URLs:

```
https://picsum.photos/id/{id}/800/600
```

- **No API key** required.
- **Content-stable** — `/id/{n}` always returns the same photo.
- **Seeded selection** — a fixed `--seed` produces the same set of IDs every run.
- **Resumable** — the cache in `testdata/generated/cache/` is content-addressed; already-
  downloaded images are never re-fetched.
- **Attribution** — photos are Unsplash-derived (Unsplash License); free use for code/tooling.
  Attribution URL stored in `manifest.json > metadata.source_attribution`.

A `source.lock.json` records the exact resolved ID list so a rerun is byte-for-byte
reproducible even if the Picsum catalogue changes.

## Manifest schema

`testdata/generated/manifest.json` is the source of truth. Key fields per sample:

| Field | Type | Description |
|---|---|---|
| `id` | string | Deterministic `<base>__<family>__<param>` |
| `base_image_id` | string | Base image identifier |
| `transform_family` | string | e.g. `jpeg_recompress` |
| `params` | object | e.g. `{"quality": 10}` |
| `expected_match` | bool | Ground-truth label |
| `confidence` | string | `"high"` or `"borderline"` |
| `borderline` | bool | Convenience bool for `confidence=="borderline"` |
| `rationale` | string | Human-readable reason for the label |
| `mime` | string | MIME type of the output file |
| `sha256` | string | SHA-256 of the output bytes |
| `is_negative_control` | bool | `true` for `different_image` samples |
| `peer_base_id` | string | Set on negative controls |

Top-level `metadata` block includes:

| Field | Description |
|---|---|
| `generator_version` | Semver of the generator at the time of generation |
| `run_id` | Unique run identifier (timestamp + short git hash) |
| `seed` | RNG seed used for reproducibility |
| `dataset_source` | `"picsum"` or `"local"` |
| `thresholds_snapshot` | Snapshot of all decision thresholds at generation time |
| `base_count` | Number of base images |
| `variants_per_base` | Number of variants per base |
| `sample_count` | `base_count × variants_per_base` |

A flat `manifest.csv` mirrors the JSON for easy spreadsheet/CLI analysis.

## Running the pipeline

### 1. Smoke (offline, no network, 20 base images)

```bash
go run ./cmd/datasetgen \
  --source local \
  --out testdata/generated \
  --seed 42
```

### 2. Full (1000 images from Lorem Picsum, ~6–10 GB disk)

```bash
go run ./cmd/datasetgen \
  --source picsum \
  --count 1000 \
  --seed 42 \
  --out testdata/generated
```

Resumable: re-run with the same flags to continue a partial download.

### 3. Evaluate (in-process, no live API needed)

```bash
# Uses testdata/generated/manifest.json (or set ALETHEIA_DATASET_MANIFEST)
go test -tags integration -run TestDataset_FullMatrix ./tests/feature/...
```

Output: stdout confusion matrix + `testdata/generated/report.json`.

### 4. Scale tips

| Scenario | Command |
|---|---|
| CI quick check | `--source local` (20 bases, offline) |
| Spot-check new threshold | `--source picsum --count 50 --seed 42` |
| Full benchmark | `--source picsum --count 1000 --seed 42` |
| Custom manifest path | `ALETHEIA_DATASET_MANIFEST=/path/to/manifest.json go test ...` |

### 5. Storage budget

~1000 bases × ~40 variants ≈ 40 000 images. Average size ~150 KB/file (JPEG);
lossless/PNG/TIFF rungs are larger (~1–2 MB each). Budget ~8–12 GB for a full run.
All generated data lives under `testdata/generated/` which is git-ignored.

## Integration with existing tests

| Test suite | What it validates | How it uses the dataset |
|---|---|---|
| `tests/domain/...` | Pure domain logic (pHash, ORB math) | No images needed |
| `tests/feature/opencv_extractor_test.go` (integration) | Curated golden oracle | `testdata/curated/aletheia/` |
| `tests/feature/dataset_matrix_test.go` (integration) | Broad matrix, smoke-base fallback | Manifest when present, else `curated/smoke-base/` |
| `tests/e2e/api_test.go` (e2e) | Full API round-trip | `testdata/curated/aletheia/` |

The **curated golden tests are never replaced** — they remain the fast, always-on, offline
regression bed for the documented boundary cases. The generated dataset adds scale.

## Testdata consolidation

All test fixtures live under a single `testdata/` root at the repository root. The
`internal/testdata` resolver resolves paths without any relative path assumptions:

```
testdata/curated/   → committed; irreplaceable hand-made oracle
testdata/generated/ → git-ignored; regenerable on demand
```

Previously images were split across `lab/hashing/testdata/` and `tests/feature/testdata/`,
referenced by fragile `../../lab/hashing/testdata` relative constants in every test file.
All consumers now use `testdata.Curated(...)` from the shared resolver.
