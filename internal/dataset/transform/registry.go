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

		// Downscale
		downscaleEntry("0.75x", 0.75, 70, true, ConfidenceHigh,
			"Downscale 75%: ORB holds >=20 inliers, scale normalised by pipeline"),
		downscaleEntry("0.5x", 0.5, 70, true, ConfidenceHigh,
			"Downscale 50%: pipeline ResizeMax=1024 normalises, ORB still reliable"),
		downscaleEntry("0.33x", 0.33, 70, true, ConfidenceHigh,
			"Downscale 33%: above min-feature threshold of ~128px for typical inputs"),
		downscaleEntry("256px", -256, 70, true, ConfidenceHigh,
			"Downscale to 256px long side: above min-feature threshold"),
		downscaleEntry("160px", -160, 70, true, ConfidenceBorderline,
			"Downscale to 160px: near minFeatureDimension=63; small images may drop inliers"),

		// Upscale
		upscaleEntry("1.5x", 1.5, 70, true, ConfidenceHigh,
			"Upscale 1.5x: resampling only, no new content"),
		upscaleEntry("2.0x", 2.0, 70, true, ConfidenceHigh,
			"Upscale 2x: resampling only, no new content"),

		// Format change (re-encode)
		formatEntry("png", "image/png", true, ConfidenceHigh,
			"Format change to PNG: lossless, zero colour residual"),
		formatEntry("gif", "image/gif", true, ConfidenceHigh,
			"Format change to GIF: palette quantisation but curated oracle confirms match"),
		formatEntry("bmp", "image/bmp", true, ConfidenceHigh,
			"Format change to BMP: lossless, identical pixel values"),
		formatEntry("tiff", "image/tiff", true, ConfidenceHigh,
			"Format change to TIFF: lossless, identical pixel values"),

		// Cardinal rotation
		rotateEntry("90", 90, true, ConfidenceHigh,
			"Rotate 90°: pHash computes 4 rotation variants; homography handles alignment"),
		rotateEntry("180", 180, true, ConfidenceHigh,
			"Rotate 180°: pHash variant covers this; homography aligns"),
		rotateEntry("270", 270, true, ConfidenceHigh,
			"Rotate 270°: pHash variant covers this; homography aligns"),

		// Small-angle rotation
		rotateEntry("5deg", 5, true, ConfidenceHigh,
			"Rotate 5°: border fill tiny, ORB robust, homography handles"),
		rotateEntry("10deg", 10, true, ConfidenceHigh,
			"Rotate 10°: moderate border fill, ORB still finds >=20 inliers"),
		rotateEntry("32deg", 32, true, ConfidenceBorderline,
			"Rotate 32°: repo confirms match; at larger angles border fill reduces inliers"),

		// Border crop
		cropEntry("5pct", 0.05, true, ConfidenceHigh,
			"Crop 5% margin: small content loss, ORB easily finds >=20 inliers"),
		cropEntry("10pct", 0.10, true, ConfidenceHigh,
			"Crop 10%: repo curated oracle confirms match at this level"),
		cropEntry("15pct", 0.15, true, ConfidenceHigh,
			"Crop 15%: within the reliable match region"),
		cropEntry("20pct", 0.20, true, ConfidenceBorderline,
			"Crop 20%: near boundary; >~25% starts dropping inliers below MinInliers=20"),

		// Brightness
		brightnessEntry("plus5pct", +5, true, ConfidenceHigh,
			"Brightness +5%: tiny L shift, LAB mean well under 8.0"),
		brightnessEntry("minus5pct", -5, true, ConfidenceHigh,
			"Brightness -5%: tiny L shift, LAB mean well under 8.0"),
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

		// Display P3 stripped ICC (colour-space mismatch)
		{
			Name: "p3_as_srgb_q70", Family: "p3_as_srgb",
			Params:        map[string]any{"quality": 70},
			ExpectedMatch: true, Confidence: ConfidenceHigh,
			Rationale: "P3 samples treated as sRGB: LAB shift < 8.0 for this quality; existing test confirms",
			MIMEType:  "image/jpeg",
		},

		// ------------------------------------------------------------------ //
		// Identity-breaking (expected_match: false)
		// ------------------------------------------------------------------ //

		// Global colour filters
		{
			Name: "grayscale", Family: "grayscale",
			Params:        map[string]any{},
			ExpectedMatch: false, Confidence: ConfidenceHigh,
			Rationale: "Full desaturation: global a/b channel collapse pushes LAB mean >> 8.0",
			MIMEType:  "image/jpeg",
		},
		{
			Name: "sepia", Family: "sepia",
			Params:        map[string]any{},
			ExpectedMatch: false, Confidence: ConfidenceHigh,
			Rationale: "Sepia matrix: strong global colour remap, repo filters produce ~11+ mean",
			MIMEType:  "image/jpeg",
		},
		hueEntry("30deg", 30, false, ConfidenceBorderline,
			"Hue shift 30°: small rotation of a/b; borderline at low saturation images"),
		hueEntry("60deg", 60, false, ConfidenceHigh,
			"Hue shift 60°: LAB mean rises above 8.0 for most images"),
		hueEntry("120deg", 120, false, ConfidenceHigh,
			"Hue shift 120°: large colour shift, well above all thresholds"),
		hueEntry("180deg", 180, false, ConfidenceHigh,
			"Hue shift 180°: complementary colours, maximum LAB residual"),

		saturationEntry("1.5x", 1.5, false, ConfidenceBorderline,
			"Saturation 1.5x: borderline; depends on image saturation baseline"),
		saturationEntry("2.0x", 2.0, false, ConfidenceHigh,
			"Saturation 2x: existing heavy_saturation_filter test confirms false"),

		{
			Name: "color_invert", Family: "color_invert",
			Params:        map[string]any{},
			ExpectedMatch: false, Confidence: ConfidenceHigh,
			Rationale: "Photographic negative: maximal colour residual, all cells spike",
			MIMEType:  "image/jpeg",
		},

		// Localised content edits
		{
			Name: "localized_recolor", Family: "localized_recolor",
			Params:        map[string]any{"region_frac": 0.10},
			ExpectedMatch: false, Confidence: ConfidenceHigh,
			Rationale: "Recolour a region (red-dress style): spikes one cell above MaxCellDist=38",
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
