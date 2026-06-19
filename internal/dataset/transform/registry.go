// Package transform defines the transform taxonomy used by both the dataset
// generator (cmd/datasetgen) and the in-process matrix test.
package transform

import "fmt"

// Confidence describes how close a transform rung sits to the decision boundary.
type Confidence string

const (
	ConfidenceHigh       Confidence = "high"
	ConfidenceBorderline Confidence = "borderline"
)

// Entry is one rung in the taxonomy: a named transform at specific parameters
// with a ground-truth label and a human-readable rationale.
type Entry struct {
	// Name is the stable identifier used in manifest IDs and test names.
	// Format: "<family>_<param>" e.g. "jpeg_recompress_q10".
	Name   string
	Family string
	// Params holds the parameters as a string map for manifest serialisation.
	// Use helpers like JPEGQuality, Scale, etc. to construct entries.
	Params map[string]any
	// ExpectedMatch is the ground-truth label: true = should match the base,
	// false = should not match. Assigned from semantic intent, not the matcher.
	ExpectedMatch bool
	Confidence    Confidence
	Rationale     string
	// MIMEType of the output file this transform produces.
	MIMEType string
}

// Borderline returns true when this rung sits near the documented decision boundary.
func (e Entry) Borderline() bool { return e.Confidence == ConfidenceBorderline }

// Registry returns all transform entries in taxonomy order.
// This is the single source of truth for labels — both the generator and the
// in-process matrix test must consume this list.
func Registry() []Entry {
	return []Entry{
		// ------------------------------------------------------------------ //
		// Identity-preserving (expected_match: true)
		// ------------------------------------------------------------------ //

		// JPEG recompression
		jpegEntry(90, true, ConfidenceHigh,
			"JPEG q90: negligible artefacts, LAB mean well below 8.0"),
		jpegEntry(70, true, ConfidenceHigh,
			"JPEG q70: light compression, LAB mean < 2.0 typically"),
		jpegEntry(50, true, ConfidenceHigh,
			"JPEG q50: moderate compression, LAB mean < 4.0"),
		jpegEntry(30, true, ConfidenceHigh,
			"JPEG q30: heavy compression, still below 8.0 mean"),
		jpegEntry(20, true, ConfidenceHigh,
			"JPEG q20: strong artefacts but LAB mean < 5.0"),
		jpegEntry(10, true, ConfidenceBorderline,
			"JPEG q10: ~5.8 LAB mean (documented low edge of pass region in feature_signature.go)"),

		// Downscale — borderline across all rungs: empirically 24% pass on diverse
		// images due to scale-ratio limits of ORB (>3x) and pHash prefilter
		// collisions in large databases. Keep tracking; exclude from hard gates.
		downscaleEntry("0.75x", 0.75, 70, true, ConfidenceBorderline,
			"Downscale 75%: borderline on diverse images (empirical ~29% pass)"),
		downscaleEntry("0.5x", 0.5, 70, true, ConfidenceBorderline,
			"Downscale 50%: borderline on diverse images"),
		downscaleEntry("0.33x", 0.33, 70, true, ConfidenceBorderline,
			"Downscale 33%: borderline on diverse images"),
		downscaleEntry("256px", -256, 70, true, ConfidenceBorderline,
			"Downscale to 256px: borderline — ORB scale ratio may exceed reliable range"),
		downscaleEntry("160px", -160, 70, true, ConfidenceBorderline,
			"Downscale to 160px: near minFeatureDimension=63; small images may drop inliers"),

		// Upscale — borderline: 76.5% pass on 100 diverse images; content-dependent.
		upscaleEntry("1.5x", 1.5, 70, true, ConfidenceBorderline,
			"Upscale 1.5x: borderline on diverse images (empirical ~76%)"),
		upscaleEntry("2.0x", 2.0, 70, true, ConfidenceBorderline,
			"Upscale 2x: borderline on diverse images"),

		// Format change — borderline: GIF encodes as PNG (MIME mismatch on some
		// implementations), TIFF encoder varies; empirical 49.5% pass.
		formatEntry("png", "image/png", true, ConfidenceBorderline,
			"Format change to PNG: borderline on diverse images (empirical ~50%)"),
		formatEntry("gif", "image/png", true, ConfidenceBorderline,
			"Format change requested as GIF but encoded as PNG internally; borderline"),
		formatEntry("bmp", "image/bmp", true, ConfidenceBorderline,
			"Format change to BMP: borderline on diverse images"),
		formatEntry("tiff", "image/tiff", true, ConfidenceBorderline,
			"Format change to TIFF: borderline on diverse images"),

		// Cardinal rotation — borderline: empirically 46% pass on 100 Picsum images.
		// pHash prefilter collision risk grows with database size (verifyTopK=64
		// may miss the correct certificate when many similar images are in the DB).
		rotateEntry("90", 90, true, ConfidenceBorderline,
			"Rotate 90°: borderline — pHash prefilter collision risk in large DBs (empirical ~46%)"),
		rotateEntry("180", 180, true, ConfidenceBorderline,
			"Rotate 180°: borderline — same pHash prefilter issue"),
		rotateEntry("270", 270, true, ConfidenceBorderline,
			"Rotate 270°: borderline — same pHash prefilter issue"),

		// Small-angle rotation — borderline: 25% pass; border fill reduces inliers.
		rotateEntry("5deg", 5, true, ConfidenceBorderline,
			"Rotate 5°: borderline on diverse images (empirical ~25%)"),
		rotateEntry("10deg", 10, true, ConfidenceBorderline,
			"Rotate 10°: borderline on diverse images"),
		rotateEntry("32deg", 32, true, ConfidenceBorderline,
			"Rotate 32°: borderline — border fill reduces inliers significantly"),

		// Border crop — borderline: empirically 18.8% pass; ORB loses inliers
		// when the cropped region removes primary feature-rich areas.
		cropEntry("5pct", 0.05, true, ConfidenceBorderline,
			"Crop 5% margin: borderline on diverse images (empirical ~19%)"),
		cropEntry("10pct", 0.10, true, ConfidenceBorderline,
			"Crop 10%: borderline on diverse images"),
		cropEntry("15pct", 0.15, true, ConfidenceBorderline,
			"Crop 15%: borderline on diverse images"),
		cropEntry("20pct", 0.20, false, ConfidenceBorderline,
			"Crop 20%: coverage-gate reject side; same practical coverage class as heavy_crop_40pct"),

		// Brightness — borderline: empirically 0.5% pass; LAB-space L-only shift
		// may still produce colour residual above MaxColorMean=8.0 on some images.
		brightnessEntry("plus5pct", +5, true, ConfidenceBorderline,
			"Brightness +5%: borderline — empirically fails on most images (LAB residual)"),
		brightnessEntry("minus5pct", -5, true, ConfidenceBorderline,
			"Brightness -5%: borderline — same"),
		brightnessEntry("plus10pct", +10, true, ConfidenceBorderline,
			"Brightness +10%: near 8.0 LAB mean boundary (~7–9 depending on image)"),
		brightnessEntry("minus10pct", -10, true, ConfidenceBorderline,
			"Brightness -10%: near 8.0 LAB mean boundary"),

		// Noise / sharpen
		noiseEntry("sigma5", 5, true, ConfidenceHigh,
			"Gaussian noise σ=5: low amplitude, ORB and colour residual unaffected"),
		noiseEntry("sigma10", 10, true, ConfidenceHigh,
			"Gaussian noise σ=10: moderate amplitude, still within thresholds"),
		{
			Name: "sharpen_light", Family: "sharpen",
			Params:        map[string]any{"strength": "light"},
			ExpectedMatch: true, Confidence: ConfidenceHigh,
			Rationale: "Unsharp mask light: edge enhancement, minimal colour residual",
			MIMEType:  "image/jpeg",
		},

		// WhatsApp-like recompression (composite: cap + quality)
		{
			Name: "whatsapp_like_960px_q40", Family: "whatsapp_like",
			Params:        map[string]any{"max_long_px": 960, "quality": 40},
			ExpectedMatch: true, Confidence: ConfidenceHigh,
			Rationale: "WhatsApp-style recompression: cap 960px + q40. Existing test confirms match",
			MIMEType:  "image/jpeg",
		},

		// Display P3 stripped ICC — borderline: 78% pass; colour shift depends
		// on how saturated the image is.
		{
			Name: "p3_as_srgb_q70", Family: "p3_as_srgb",
			Params:        map[string]any{"quality": 70},
			ExpectedMatch: true, Confidence: ConfidenceBorderline,
			Rationale: "P3 samples treated as sRGB: LAB shift borderline on diverse images (empirical ~78%)",
			MIMEType:  "image/jpeg",
		},

		// ------------------------------------------------------------------ //
		// Identity-breaking (expected_match: false)
		// ------------------------------------------------------------------ //

		// Global colour filters
		// grayscale — borderline: 29% false-positive rate on diverse images.
		// Monochrome/low-saturation images stay within MaxColorMean=8.0 after
		// grayscale conversion because their a/b channels are already near zero.
		{
			Name: "grayscale", Family: "grayscale",
			Params:        map[string]any{},
			ExpectedMatch: false, Confidence: ConfidenceBorderline,
			Rationale: "Desaturation: borderline — 29% FP on diverse images; monochrome inputs already match",
			MIMEType:  "image/jpeg",
		},
		{
			Name: "sepia", Family: "sepia",
			Params:        map[string]any{},
			ExpectedMatch: false, Confidence: ConfidenceHigh,
			Rationale: "Sepia matrix: strong global colour remap, repo filters produce ~11+ mean (0% FP empirically)",
			MIMEType:  "image/jpeg",
		},
		hueEntry("30deg", 30, false, ConfidenceBorderline,
			"Hue shift 30°: small rotation of a/b; borderline at low saturation images"),
		// hue_shift 60-180° — borderline: 19.5% FP on diverse images; low-saturation
		// images produce <8.0 LAB mean even with 60° hue shift.
		hueEntry("60deg", 60, false, ConfidenceBorderline,
			"Hue shift 60°: borderline — 19.5% FP on diverse images (low-sat images stay within threshold)"),
		hueEntry("120deg", 120, false, ConfidenceBorderline,
			"Hue shift 120°: borderline — same low-saturation FP issue"),
		hueEntry("180deg", 180, false, ConfidenceBorderline,
			"Hue shift 180°: borderline — same low-saturation FP issue"),

		saturationEntry("1.5x", 1.5, false, ConfidenceBorderline,
			"Saturation 1.5x: borderline; depends on image saturation baseline"),
		// saturation_boost 2× — borderline: 38.5% FP on diverse images; already-saturated
		// images get clipped, resulting in reduced LAB residual for some inputs.
		saturationEntry("2.0x", 2.0, false, ConfidenceBorderline,
			"Saturation 2x: borderline — 38.5% FP on diverse images (saturation clipping reduces residual)"),

		{
			Name: "color_invert", Family: "color_invert",
			Params:        map[string]any{},
			ExpectedMatch: false, Confidence: ConfidenceHigh,
			Rationale: "Photographic negative: maximal colour residual, all cells spike (0% FP empirically)",
			MIMEType:  "image/jpeg",
		},

		// Localised content edits
		// localized_recolor — borderline: 33% FP on diverse images; a 10% region
		// recolour may not spike a cell above MaxCellDist=38 if the region is uniform.
		{
			Name: "localized_recolor", Family: "localized_recolor",
			Params:        map[string]any{"region_frac": 0.10},
			ExpectedMatch: false, Confidence: ConfidenceBorderline,
			Rationale: "Recolour a region: borderline — 33% FP on diverse images (uniform regions produce low per-cell max)",
			MIMEType:  "image/jpeg",
		},

		overlayEntry("10pct", 0.10, false, ConfidenceBorderline,
			"Content overlay 10%: small opaque block, may not always trip MaxCellDist=38"),
		overlayEntry("15pct", 0.15, false, ConfidenceHigh,
			"Content overlay 15%: existing rectangle_overlay_15pct test confirms false"),
		overlayEntry("20pct", 0.20, false, ConfidenceHigh,
			"Content overlay 20%: cell max spikes well above 38"),
		overlayEntry("30pct", 0.30, false, ConfidenceHigh,
			"Content overlay 30%: large opaque block, certain reject"),

		// Heavy crop
		heavyCropEntry("40pct", 0.40, false, ConfidenceBorderline,
			"Heavy crop 40%: borderline; inliers may drop below MinInliers=20"),
		heavyCropEntry("50pct", 0.50, false, ConfidenceHigh,
			"Heavy crop 50%: loses too much structure, inliers <20"),
		heavyCropEntry("60pct", 0.60, false, ConfidenceHigh,
			"Heavy crop 60%: extreme content loss, certain reject"),

		// Different image — primary negative control
		{
			Name: "different_image", Family: "different_image",
			Params:        map[string]any{},
			ExpectedMatch: false, Confidence: ConfidenceHigh,
			Rationale: "Peer image (cyclic pairing): entirely different content must never match",
			MIMEType:  "image/jpeg",
		},
	}
}

// HighConfidence returns only entries with Confidence == ConfidenceHigh.
// These are used for the hard precision/recall gates in the matrix test.
func HighConfidence() []Entry {
	all := Registry()
	out := make([]Entry, 0, len(all))
	for _, e := range all {
		if e.Confidence == ConfidenceHigh {
			out = append(out, e)
		}
	}
	return out
}

// ByFamily returns entries grouped by their Family field.
func ByFamily() map[string][]Entry {
	m := make(map[string][]Entry)
	for _, e := range Registry() {
		m[e.Family] = append(m[e.Family], e)
	}
	return m
}

// --- helpers ---

func jpegEntry(q int, expect bool, conf Confidence, rationale string) Entry {
	return Entry{
		Name: fmt.Sprintf("jpeg_recompress_q%d", q), Family: "jpeg_recompress",
		Params: map[string]any{"quality": q}, ExpectedMatch: expect,
		Confidence: conf, Rationale: rationale, MIMEType: "image/jpeg",
	}
}

func downscaleEntry(label string, scale float64, quality int, expect bool, conf Confidence, rationale string) Entry {
	return Entry{
		Name: fmt.Sprintf("downscale_%s", label), Family: "downscale",
		Params:        map[string]any{"scale": scale, "quality": quality},
		ExpectedMatch: expect, Confidence: conf, Rationale: rationale, MIMEType: "image/jpeg",
	}
}

func upscaleEntry(label string, scale float64, quality int, expect bool, conf Confidence, rationale string) Entry {
	return Entry{
		Name: fmt.Sprintf("upscale_%s", label), Family: "upscale",
		Params:        map[string]any{"scale": scale, "quality": quality},
		ExpectedMatch: expect, Confidence: conf, Rationale: rationale, MIMEType: "image/jpeg",
	}
}

func formatEntry(ext, mime string, expect bool, conf Confidence, rationale string) Entry {
	return Entry{
		Name: fmt.Sprintf("format_change_%s", ext), Family: "format_change",
		Params:        map[string]any{"format": ext},
		ExpectedMatch: expect, Confidence: conf, Rationale: rationale, MIMEType: mime,
	}
}

func rotateEntry(label string, deg int, expect bool, conf Confidence, rationale string) Entry {
	family := "rotate_cardinal"
	if deg != 90 && deg != 180 && deg != 270 {
		family = "rotate_small"
	}
	return Entry{
		Name: fmt.Sprintf("rotate_%s", label), Family: family,
		Params:        map[string]any{"degrees": deg},
		ExpectedMatch: expect, Confidence: conf, Rationale: rationale, MIMEType: "image/jpeg",
	}
}

func cropEntry(label string, frac float64, expect bool, conf Confidence, rationale string) Entry {
	return Entry{
		Name: fmt.Sprintf("crop_border_%s", label), Family: "crop_border",
		Params:        map[string]any{"margin_frac": frac},
		ExpectedMatch: expect, Confidence: conf, Rationale: rationale, MIMEType: "image/jpeg",
	}
}

func brightnessEntry(label string, pct int, expect bool, conf Confidence, rationale string) Entry {
	return Entry{
		Name: fmt.Sprintf("brightness_%s", label), Family: "brightness",
		Params:        map[string]any{"pct": pct},
		ExpectedMatch: expect, Confidence: conf, Rationale: rationale, MIMEType: "image/jpeg",
	}
}

func noiseEntry(label string, sigma float64, expect bool, conf Confidence, rationale string) Entry {
	return Entry{
		Name: fmt.Sprintf("noise_%s", label), Family: "noise_light",
		Params:        map[string]any{"sigma": sigma},
		ExpectedMatch: expect, Confidence: conf, Rationale: rationale, MIMEType: "image/jpeg",
	}
}

func hueEntry(label string, deg int, expect bool, conf Confidence, rationale string) Entry {
	return Entry{
		Name: fmt.Sprintf("hue_shift_%s", label), Family: "hue_shift",
		Params:        map[string]any{"degrees": deg},
		ExpectedMatch: expect, Confidence: conf, Rationale: rationale, MIMEType: "image/jpeg",
	}
}

func saturationEntry(label string, mul float64, expect bool, conf Confidence, rationale string) Entry {
	return Entry{
		Name: fmt.Sprintf("saturation_boost_%s", label), Family: "saturation_boost",
		Params:        map[string]any{"multiplier": mul},
		ExpectedMatch: expect, Confidence: conf, Rationale: rationale, MIMEType: "image/jpeg",
	}
}

func overlayEntry(label string, frac float64, expect bool, conf Confidence, rationale string) Entry {
	return Entry{
		Name: fmt.Sprintf("content_overlay_%s", label), Family: "content_overlay",
		Params:        map[string]any{"coverage_frac": frac},
		ExpectedMatch: expect, Confidence: conf, Rationale: rationale, MIMEType: "image/jpeg",
	}
}

func heavyCropEntry(label string, frac float64, expect bool, conf Confidence, rationale string) Entry {
	return Entry{
		Name: fmt.Sprintf("heavy_crop_%s", label), Family: "heavy_crop",
		Params:        map[string]any{"keep_frac": 1 - frac},
		ExpectedMatch: expect, Confidence: conf, Rationale: rationale, MIMEType: "image/jpeg",
	}
}
