//go:build integration

package feature_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/waizbart/aletheia-api/internal/dataset/transform"
	"github.com/waizbart/aletheia-api/internal/feature"
)

// TestOpenCVExtractor_BrightnessLumaOffset locks in the luminance-offset
// neutralization in colorResidual. A global brightness/exposure shift is an
// identity-preserving edit our taxonomy labels as a match, but it moves every
// cell's L channel and historically inflated the LAB mean past MaxColorMean,
// rejecting the match. After neutralizing the global ΔL, brightness rungs must
// match while chroma-changing edits stay rejected.
func TestOpenCVExtractor_BrightnessLumaOffset(t *testing.T) {
	ctx := context.Background()
	ext := feature.NewOpenCVExtractor()
	defer ext.Close()

	refBytes, err := os.ReadFile(filepath.Join(testdataDir, referenceFile))
	if err != nil {
		t.Fatalf("read reference: %v", err)
	}
	refSig, err := ext.Compute(ctx, refBytes)
	if err != nil {
		t.Fatalf("compute reference: %v", err)
	}

	byName := make(map[string]transform.Entry)
	for _, e := range transform.Registry() {
		byName[e.Name] = e
	}

	cases := []struct {
		entryName string
		want      bool
	}{
		{"brightness_plus5pct", true},
		{"brightness_minus5pct", true},
		{"brightness_plus10pct", true},
		{"brightness_minus10pct", true},
		// Chroma-changing controls must remain rejected after the L fix.
		{"sepia", false},
		{"color_invert", false},
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
			candSig, cerr := ext.Compute(ctx, candBytes)
			if cerr != nil {
				t.Fatalf("compute %s: %v", tc.entryName, cerr)
			}
			dec, merr := ext.Match(ctx, refSig, candSig, candBytes)
			if merr != nil {
				t.Fatalf("match %s: %v", tc.entryName, merr)
			}
			t.Logf("%s: matched=%v mean=%.2f max=%.2f coverage=%.3f inliers=%d",
				tc.entryName, dec.Matched, dec.ColorMean, dec.ColorMax, dec.Coverage, dec.Inliers)
			if dec.Matched != tc.want {
				t.Errorf("%s matched=%v want=%v (mean=%.2f max=%.2f)",
					tc.entryName, dec.Matched, tc.want, dec.ColorMean, dec.ColorMax)
			}
		})
	}
}
