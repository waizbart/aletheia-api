//go:build integration

package feature_test

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/feature"
)

// loadRef reads the golden reference image shared by the extractor tests.
func loadRef(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testdataDir, referenceFile))
	if err != nil {
		t.Fatalf("read reference: %v", err)
	}
	return b
}

// TestCompute_ColorGrid verifies certify-time grid extraction: shape, dims,
// and that the grid carries real color information (not a constant fill).
func TestCompute_ColorGrid(t *testing.T) {
	ext := feature.NewOpenCVExtractor()
	defer ext.Close()
	ctx := context.Background()

	refBytes := loadRef(t)
	sig, err := ext.Compute(ctx, refBytes)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}

	if !sig.HasColorGrid() {
		t.Fatalf("signature has no valid color grid: %d bytes, %dx%d",
			len(sig.ColorGrid), sig.RefWidth, sig.RefHeight)
	}
	if len(sig.ColorGrid) != domain.ColorGridBytes {
		t.Fatalf("grid length = %d, want %d", len(sig.ColorGrid), domain.ColorGridBytes)
	}
	if sig.RefWidth > domain.ResizeMax || sig.RefHeight > domain.ResizeMax {
		t.Fatalf("ref dims %dx%d exceed ResizeMax %d", sig.RefWidth, sig.RefHeight, domain.ResizeMax)
	}

	// A real photo must produce varied cell means. A constant grid would mean
	// the cell rectangles or channel indexing are broken.
	first := sig.ColorGrid[0]
	varied := false
	for _, b := range sig.ColorGrid {
		if b != first {
			varied = true
			break
		}
	}
	if !varied {
		t.Fatal("color grid is a constant fill — cell means are not being computed")
	}

	// Determinism: certify-time grids are stored and compared across processes,
	// so recomputing on the same bytes must be byte-identical.
	sig2, err := ext.Compute(ctx, refBytes)
	if err != nil {
		t.Fatalf("recompute: %v", err)
	}
	if !bytes.Equal(sig.ColorGrid, sig2.ColorGrid) {
		t.Fatal("color grid is not deterministic for identical input")
	}
	if sig.RefWidth != sig2.RefWidth || sig.RefHeight != sig2.RefHeight {
		t.Fatalf("ref dims not deterministic: %dx%d vs %dx%d",
			sig.RefWidth, sig.RefHeight, sig2.RefWidth, sig2.RefHeight)
	}
}

// TestMatch_SelfMatchWithStoredGrid is the replacement pipeline's core parity
// property: matching an image against its own stored signature (grid included,
// no reference image anywhere) must pass every gate with a near-zero residual.
func TestMatch_SelfMatchWithStoredGrid(t *testing.T) {
	ext := feature.NewOpenCVExtractor()
	defer ext.Close()
	ctx := context.Background()

	refBytes := loadRef(t)
	refSig, err := ext.Compute(ctx, refBytes)
	if err != nil {
		t.Fatalf("compute ref: %v", err)
	}
	candSig, err := ext.Compute(ctx, refBytes)
	if err != nil {
		t.Fatalf("compute cand: %v", err)
	}

	dec, err := ext.Match(ctx, refSig, candSig, refBytes)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if !dec.Matched {
		t.Fatalf("self-match failed: %+v", dec)
	}
	// The stored grid quantizes each channel mean to the nearest integer, so
	// the self-match residual is bounded by the quantization error (≤ ~0.87
	// per cell), far below the 8.0 threshold.
	if dec.ColorMean > 1.0 {
		t.Errorf("self-match ColorMean = %.3f, want ≤ 1.0 (quantization bound)", dec.ColorMean)
	}
	if dec.Coverage < 0.99 {
		t.Errorf("self-match coverage = %.3f, want ≈ 1.0", dec.Coverage)
	}
}

// TestMatch_RequiresColorGrid pins the contract: a reference signature without
// a valid grid is an error, not a silent pass — verify relies on this to skip
// legacy candidates instead of mis-matching them.
func TestMatch_RequiresColorGrid(t *testing.T) {
	ext := feature.NewOpenCVExtractor()
	defer ext.Close()
	ctx := context.Background()

	refBytes := loadRef(t)
	sig, err := ext.Compute(ctx, refBytes)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}

	bare := &domain.FeatureSignature{
		Descriptors: sig.Descriptors,
		Keypoints:   sig.Keypoints,
	}
	if _, err := ext.Match(ctx, bare, sig, refBytes); err == nil {
		t.Fatal("expected error matching against a signature without a color grid")
	}

	malformed := &domain.FeatureSignature{
		Descriptors: sig.Descriptors,
		Keypoints:   sig.Keypoints,
		ColorGrid:   []byte{1, 2, 3},
		RefWidth:    sig.RefWidth,
		RefHeight:   sig.RefHeight,
	}
	if _, err := ext.Match(ctx, malformed, sig, refBytes); err == nil {
		t.Fatal("expected error matching against a malformed color grid")
	}
}

// TestRenderColorGridPNG covers the dashboard thumbnail path: a stored grid
// renders to a decodable PNG at the reference aspect ratio, and invalid
// inputs are rejected.
func TestRenderColorGridPNG(t *testing.T) {
	ext := feature.NewOpenCVExtractor()
	defer ext.Close()
	ctx := context.Background()

	refBytes := loadRef(t)
	sig, err := ext.Compute(ctx, refBytes)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}

	pngBytes, err := ext.RenderColorGridPNG(sig.ColorGrid, sig.RefWidth, sig.RefHeight)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("output is not decodable PNG: %v", err)
	}

	b := img.Bounds()
	wantRatio := float64(sig.RefWidth) / float64(sig.RefHeight)
	gotRatio := float64(b.Dx()) / float64(b.Dy())
	if diff := wantRatio - gotRatio; diff < -0.05 || diff > 0.05 {
		t.Errorf("thumbnail aspect ratio %.3f, want ≈ %.3f", gotRatio, wantRatio)
	}

	if _, err := ext.RenderColorGridPNG([]byte{1, 2, 3}, 100, 100); err == nil {
		t.Error("expected error for malformed grid")
	}
	if _, err := ext.RenderColorGridPNG(sig.ColorGrid, 0, 100); err == nil {
		t.Error("expected error for zero width")
	}
}

// TestMatch_SepiaRejectedViaStoredGrid guards the tamper-detection property
// the grid must preserve: a global color filter keeps ORB geometry intact but
// must fail the color gate computed from the stored grid.
func TestMatch_SepiaRejectedViaStoredGrid(t *testing.T) {
	ext := feature.NewOpenCVExtractor()
	defer ext.Close()
	ctx := context.Background()

	refBytes := loadRef(t)
	refSig, err := ext.Compute(ctx, refBytes)
	if err != nil {
		t.Fatalf("compute ref: %v", err)
	}

	sepiaBytes := sepiaJPEG(t, refBytes)
	candSig, err := ext.Compute(ctx, sepiaBytes)
	if err != nil {
		t.Fatalf("compute sepia: %v", err)
	}

	dec, err := ext.Match(ctx, refSig, candSig, sepiaBytes)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if dec.Matched {
		t.Fatalf("sepia filter passed the color gate: %+v", dec)
	}
	if dec.Inliers < domain.MinInliers {
		t.Logf("note: sepia rejected on geometry (inliers=%d) — color gate not exercised", dec.Inliers)
	}
}

// sepiaJPEG applies a standard sepia matrix in Go image space and re-encodes.
func sepiaJPEG(t *testing.T, src []byte) []byte {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	b := img.Bounds()
	out := image.NewRGBA(b)
	clamp := func(v float64) uint8 {
		if v > 255 {
			return 255
		}
		return uint8(v)
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			rf, gf, bf := float64(r>>8), float64(g>>8), float64(bl>>8)
			out.Pix[out.PixOffset(x, y)+0] = clamp(0.393*rf + 0.769*gf + 0.189*bf)
			out.Pix[out.PixOffset(x, y)+1] = clamp(0.349*rf + 0.686*gf + 0.168*bf)
			out.Pix[out.PixOffset(x, y)+2] = clamp(0.272*rf + 0.534*gf + 0.131*bf)
			out.Pix[out.PixOffset(x, y)+3] = 255
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode sepia: %v", err)
	}
	return buf.Bytes()
}
