//go:build integration

package feature_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/waizbart/aletheia-api/internal/dataset/transform"
	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/feature"
)

// TestOpenCVExtractor_CoverageGate locks in the area-coverage gate that
// separates legitimate border crops (which preserve most of the frame and must
// match) from identity-breaking heavy crops (which discard a large share of the
// frame and must NOT match). Heavy crops are pixel-identical inside the surviving
// region, so without the coverage gate they pass every inlier and colour check.
//
// It drives the real transform builders against the curated reference and
// asserts the decision per rung, while logging the measured Coverage so the
// MinAreaCoverage threshold stays auditable.
func TestOpenCVExtractor_CoverageGate(t *testing.T) {
	ctx := context.Background()
	ext := feature.NewOpenCVExtractor()
	defer ext.Close()

	refBytes, err := os.ReadFile(filepath.Join(testdataDir, referenceFile))
	if err != nil {
		t.Fatalf("read reference: %v", err)
	}
	refSig, refImage, err := ext.Compute(ctx, refBytes)
	if err != nil {
		t.Fatalf("compute reference: %v", err)
	}

	// Transform rungs spanning both sides of the coverage boundary. want is the
	// decision the gate SHOULD produce, which is purely a function of how much
	// frame area survives the crop.
	//
	// NOTE: crop_border_20pct (margin 0.20 per side → keep 0.6 linear → 0.36
	// area) and heavy_crop_40pct (keep_frac 0.60 → 0.36 area) are the SAME
	// pixel-identical image, yet transform.Registry labels them true vs false.
	// That is a ground-truth contradiction no threshold can satisfy; the gate
	// resolves it the safe way — a crop discarding ~64% of the frame is
	// identity-breaking, so both reject.
	cases := []struct {
		entryName string
		want      bool
	}{
		{"crop_border_5pct", true},   // 0.79 coverage
		{"crop_border_10pct", true},  // 0.64
		{"crop_border_15pct", true},  // 0.48
		{"crop_border_20pct", false}, // 0.35 — identical to heavy_crop_40pct
		{"heavy_crop_40pct", false},  // 0.35
		{"heavy_crop_50pct", false},  // 0.25
		{"heavy_crop_60pct", false},  // 0.15
	}

	byName := make(map[string]transform.Entry)
	for _, e := range transform.Registry() {
		byName[e.Name] = e
	}

	for _, tc := range cases {
		t.Run(tc.entryName, func(t *testing.T) {
			entry, ok := byName[tc.entryName]
			if !ok {
				t.Fatalf("entry %s not in registry", tc.entryName)
			}
			build, berr := transform.BuilderFor(entry)
			if berr != nil {
				t.Fatalf("builder for %s: %v", tc.entryName, berr)
			}
			candBytes, buildErr := build(refBytes, refBytes)
			if buildErr != nil {
				t.Fatalf("build %s: %v", tc.entryName, buildErr)
			}
			candSig, _, cerr := ext.Compute(ctx, candBytes)
			if cerr != nil {
				t.Fatalf("compute %s: %v", tc.entryName, cerr)
			}
			dec, merr := ext.Match(ctx, refSig, candSig, refImage, candBytes)
			if merr != nil {
				t.Fatalf("match %s: %v", tc.entryName, merr)
			}
			t.Logf("%s: matched=%v coverage=%.3f (gate=%.2f) inliers=%d mean=%.2f max=%.2f",
				tc.entryName, dec.Matched, dec.Coverage, domain.MinAreaCoverage,
				dec.Inliers, dec.ColorMean, dec.ColorMax)
			if dec.Matched != tc.want {
				t.Errorf("%s matched=%v want=%v (coverage=%.3f gate=%.2f)",
					tc.entryName, dec.Matched, tc.want, dec.Coverage, domain.MinAreaCoverage)
			}
		})
	}
}
