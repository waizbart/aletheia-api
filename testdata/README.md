# testdata/

Shared test fixtures for all tests and lab tools in this repository.

## Layout

```text
testdata/
  curated/               ← committed; hand-made, irreplaceable oracle
    aletheia/            ← 28 aletheia-* images (golden quality/rotation/filter matrix)
    realworld/           ← glass-rail-* real WhatsApp recompression photos
    smoke-base/          ← 20 img_NNN.jpg base photos (offline fallback for the matrix test)
  generated/             ← git-ignored; downloaded + derived, fully regenerable
    base/                ← 1000 Picsum base images
    cache/               ← content-addressed download cache (resumable)
    variants/<base_id>/  ← per-base transform variants
    manifest.json        ← ground-truth manifest (source of truth)
    manifest.csv         ← flat CSV mirror of manifest.json
    report.json          ← confusion-matrix output (produced by the eval test)
```

## How to access paths from code

Use the `internal/testdata` resolver — it works from any working directory:

```go
import "github.com/waizbart/aletheia-api/internal/testdata"

dir  := testdata.Curated("aletheia")              // .../testdata/curated/aletheia
file := testdata.Curated("aletheia", "aletheia.jpg")
gen  := testdata.Generated("manifest.json")       // .../testdata/generated/manifest.json
```

`testdata.ManifestPath()` returns `generated/manifest.json` unless
`ALETHEIA_DATASET_MANIFEST` is set in the environment.

## How to regenerate the dataset

### Smoke run (offline, 20 base images from `curated/smoke-base`)

```bash
go run -tags datasetgen ./cmd/datasetgen \
  --source local \
  --out testdata/generated \
  --seed 42
```

### Full run (1000 images from Lorem Picsum)

```bash
go run -tags datasetgen ./cmd/datasetgen \
  --source picsum \
  --count 1000 \
  --seed 42 \
  --out testdata/generated
```

The generator is **resumable**: if interrupted, re-running with the same `--seed`
and `--out` skips already-downloaded images and already-generated variants.

## How to run the matrix evaluation

```bash
# In-process eval against generated manifest (no live API needed)
go test -tags integration -run TestDataset_FullMatrix ./tests/feature/...

# With a custom manifest
ALETHEIA_DATASET_MANIFEST=testdata/generated/manifest.json \
  go test -tags integration -run TestDataset_FullMatrix ./tests/feature/...
```

Results are printed to stdout and written to `testdata/generated/report.json`.

## Transform taxonomy

See `internal/dataset/transform/registry.go` for the full canonical list. Summary:

| Family | Expected match | Confidence | Notes |
|---|---|---|---|
| jpeg_recompress q90–q20 | true | high | |
| jpeg_recompress q10 | true | borderline | ≈5.8 LAB mean, near low edge |
| downscale 0.75–0.33, →256px, →160px | true | borderline | scale-ratio and prefilter recall are content-dependent |
| upscale 1.5x–2x | true | borderline | resampling only, but broad-image recall varies |
| format_change png/gif-request-as-png/bmp/tiff | true | borderline | encoder/decoder differences vary by image |
| rotate_cardinal 90/180/270° | true | borderline | pHash rotation variants help, but large DB prefilter can miss |
| rotate_small 5/10/32° | true | borderline | border fill reduces inliers |
| crop_border 5/10/15% | true | borderline | crops can remove feature-rich regions |
| brightness ±5/±10% | true | borderline | LAB residual often crosses current threshold |
| noise_light σ5/σ10 | true | high | |
| sharpen light | true | high | |
| whatsapp_like 960px q40 | true | high | |
| p3_as_srgb q70 | true | borderline | depends on source saturation |
| grayscale | false | borderline | low-saturation inputs can stay within threshold |
| sepia | false | high | |
| hue_shift 30/60/120/180° | false | borderline | low-saturation inputs can stay within threshold |
| saturation_boost 1.5x/2x | false | borderline | depends on source saturation and clipping |
| color_invert | false | high | |
| localized_recolor | false | borderline | 10% recolor may not spike a grid cell enough |
| content_overlay 15/20/30% | false | high | |
| content_overlay 10% | false | borderline | |
| crop_border 20% | false | borderline | coverage-gate reject side |
| heavy_crop 50/60% | false | high | |
| heavy_crop 40% | false | borderline | |
| different_image (peer) | false | high | primary negative control |

## Ground-truth principle

Labels are assigned **a priori** from the semantic intent of each transform
(what the matcher *should* return), independently of the algorithm. This makes
the dataset a genuine benchmark rather than a tautology.

`confidence: "borderline"` marks rungs near the documented decision boundary.
The eval reports these separately and does not count them in precision/recall gates.

The manifest embeds a `thresholds_snapshot` so a future reader can detect when
labels were authored against different threshold values than the current code.
