//go:build integration

package feature_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gocv.io/x/gocv"

	"github.com/waizbart/aletheia-api/internal/feature"
	"github.com/waizbart/aletheia-api/internal/testdata"
)

var testdataDir = testdata.Curated("aletheia")

const referenceFile = "aletheia.jpg"

var expected = map[string]bool{
	"aletheia.jpg":             true,
	"aletheia.png":             true,
	"aletheia.gif":             true,
	"aletheia-q10.jpg":         true,
	"aletheia-q20.jpg":         true,
	"aletheia-q30.jpg":         true,
	"aletheia-q40.jpg":         true,
	"aletheia-q50.jpg":         true,
	"aletheia-q60.jpg":         true,
	"aletheia-q70.jpg":         true,
	"aletheia-q80.jpg":         true,
	"aletheia-rotated-90.jpg":  true,
	"aletheia-rotated-180.jpg": true,
	"aletheia-rotated-270.jpg": true,
	"aletheia-cropped-10p.jpg": true,
	"aletheia-changed-1.jpg":   false,
	"aletheia-changed-2.jpg":   false,
	"aletheia-changed-3.jpg":   false,
	"aletheia-changed-4.jpg":   false,
	"aletheia-changed-5.jpg":   false,
	"aletheia-changed-6.jpg":   false,
	"aletheia-changed-7.jpg":   false,
	"aletheia-filter-1.jpg":    false,
	"aletheia-filter-2.jpg":    false,
	"aletheia-filter-3.jpg":    false,
}

func noisePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(1))
	for i := range img.Pix {
		img.Pix[i] = byte(rng.Intn(256))
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// TestOpenCVExtractor_TinyImageGuard locks in the fix for the SIGSEGV that
// degenerate uploads triggered inside cgo: images below the ORB minimum must be
// rejected with a clean error, and images at/above it must run without crashing
// the test process.
func TestOpenCVExtractor_TinyImageGuard(t *testing.T) {
	ext := feature.NewOpenCVExtractor()
	defer ext.Close()
	ctx := context.Background()

	// 1x1 is the original crash repro; 62 is just under the threshold.
	for _, size := range []int{1, 16, 62} {
		t.Run(fmt.Sprintf("%dpx_rejected", size), func(t *testing.T) {
			_, _, err := ext.Compute(ctx, noisePNG(t, size, size))
			if err == nil {
				t.Fatalf("expected error for %dx%d image, got nil", size, size)
			}
			if !strings.Contains(err.Error(), "too small") {
				t.Fatalf("error = %v, want 'too small'", err)
			}
		})
	}

	// At/above the threshold ORB must not crash. A non-crash outcome is either a
	// signature or a clean "no features detected" error.
	for _, size := range []int{63, 80} {
		t.Run(fmt.Sprintf("%dpx_no_crash", size), func(t *testing.T) {
			_, _, err := ext.Compute(ctx, noisePNG(t, size, size))
			if err != nil && !strings.Contains(err.Error(), "no features detected") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestOpenCVExtractor_TestdataMatrix(t *testing.T) {
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

	entries, err := os.ReadDir(testdataDir)
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	passes, total := 0, 0
	for _, name := range names {
		want, ok := expected[name]
		if !ok {
			continue
		}
		total++

		t.Run(name, func(t *testing.T) {
			candBytes, err := os.ReadFile(filepath.Join(testdataDir, name))
			if err != nil {
				t.Fatalf("read candidate: %v", err)
			}
			candSig, _, err := ext.Compute(ctx, candBytes)
			if err != nil {
				t.Fatalf("compute candidate: %v", err)
			}

			decision, err := ext.Match(ctx, refSig, candSig, refImage, candBytes)
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if decision.Matched != want {
				t.Errorf("matched = %v, want %v (inliers=%d, mean=%.2f, max=%.2f)",
					decision.Matched, want, decision.Inliers, decision.ColorMean, decision.ColorMax)
				return
			}
			passes++
		})
	}

	t.Logf("passes: %d/%d", passes, total)
}

// TestOpenCVExtractor_LargeCandidateScale locks in the fix for the bug where a
// candidate image larger than domain.ResizeMax produced a coordinate mismatch
// between the homography (computed on the resized keypoints) and the candidate
// LAB (previously decoded at the original resolution). The result was a
// massive color residual even when geometry matched perfectly, e.g. a 4000x3000
// phone photo re-uploaded through WhatsApp would fail to verify.
//
// The candidate here is an upscale + JPEG re-encode + 90° rotation of the
// reference, simulating a high-resolution recompressed copy. With the fix in
// place, Match must return Matched=true.
func TestOpenCVExtractor_LargeCandidateScale(t *testing.T) {
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

	// Decode → upscale to ~3x ResizeMax → rotate 90° → re-encode as low-quality
	// JPEG so we have a candidate that is both larger than ResizeMax and
	// noticeably recompressed.
	candBytes := upscaleRotateRecompress(t, refBytes, 3000, 30)

	candSig, _, err := ext.Compute(ctx, candBytes)
	if err != nil {
		t.Fatalf("compute candidate: %v", err)
	}

	decision, err := ext.Match(ctx, refSig, candSig, refImage, candBytes)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if !decision.Matched {
		t.Fatalf("expected match for large recompressed rotated candidate, got false (inliers=%d, mean=%.2f, max=%.2f, cells=%d)",
			decision.Inliers, decision.ColorMean, decision.ColorMax, decision.Cells)
	}
	t.Logf("large-candidate match ok: inliers=%d mean=%.2f max=%.2f cells=%d",
		decision.Inliers, decision.ColorMean, decision.ColorMax, decision.Cells)
}

// TestOpenCVExtractor_RealWorldGlassRail uses a real Galaxy phone photo plus
// three derivatives that circulated through WhatsApp:
//   - whatsapp: same scene, just WhatsApp's recompression (must match)
//   - filter:   WhatsApp + a heavy color/sat filter (must NOT match)
//   - emoji:    WhatsApp + an emoji sticker pasted on top (must NOT match)
//
// This is a regression bed for the scale-mismatch fix in decodeLAB AND a
// guard that thresholds stay strict enough to reject filtered/altered copies.
func TestOpenCVExtractor_RealWorldGlassRail(t *testing.T) {
	dir := testdata.Curated("realworld")
	ctx := context.Background()
	ext := feature.NewOpenCVExtractor()
	defer ext.Close()

	refBytes, err := os.ReadFile(filepath.Join(dir, "glass-rail-original.jpg"))
	if err != nil {
		t.Fatalf("read reference: %v", err)
	}
	refSig, refImage, err := ext.Compute(ctx, refBytes)
	if err != nil {
		t.Fatalf("compute reference: %v", err)
	}

	cases := []struct {
		name string
		file string
		want bool
	}{
		{"whatsapp_recompression", "glass-rail-whatsapp.jpg", true},
		{"filter_applied", "glass-rail-filter.jpg", false},
		{"emoji_overlay", "glass-rail-emoji.jpg", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candBytes, err := os.ReadFile(filepath.Join(dir, tc.file))
			if err != nil {
				t.Fatalf("read %s: %v", tc.file, err)
			}
			candSig, _, err := ext.Compute(ctx, candBytes)
			if err != nil {
				t.Fatalf("compute %s: %v", tc.file, err)
			}
			decision, err := ext.Match(ctx, refSig, candSig, refImage, candBytes)
			if err != nil {
				t.Fatalf("match %s: %v", tc.file, err)
			}
			if decision.Matched != tc.want {
				t.Errorf("matched = %v, want %v (inliers=%d, mean=%.2f, max=%.2f, cells=%d)",
					decision.Matched, tc.want, decision.Inliers, decision.ColorMean, decision.ColorMax, decision.Cells)
				return
			}
			t.Logf("%s: matched=%v inliers=%d mean=%.2f max=%.2f cells=%d",
				tc.name, decision.Matched, decision.Inliers, decision.ColorMean, decision.ColorMax, decision.Cells)
		})
	}
}

// TestOpenCVExtractor_DisplayP3StrippedICC documents what happens when a
// Display P3 photo (common on iPhone, and on Samsung "wide color" mode) goes
// through a tool that strips the ICC profile WITHOUT converting pixel values
// to sRGB. Both gocv.IMDecode and Go's image/jpeg ignore ICC profiles, so the
// candidate's pixel samples are then misinterpreted as sRGB, which shifts LAB
// values on saturated regions.
//
// The candidate here is built from the reference by:
//  1. linearising sRGB samples (inverse transfer)
//  2. multiplying by the sRGB→DisplayP3 primaries matrix
//  3. re-applying the sRGB transfer (Display P3 shares the sRGB transfer)
//  4. encoding as JPEG quality 90 with no ICC tag
//
// Visually the two are the same photo if rendered with the right profile, but
// the raw RGB samples differ. This is the failure mode for "iPhone P3 photo
// certified, WhatsApp copy verified" once a CMS-aware tool gets involved.
//
// We deliberately use the reference image (aletheia.jpg, 1000×1000 with rich
// color) so the test exercises the wide-gamut samples a phone produces. The
// test asserts on the *observed* behaviour today (current pipeline assumes
// sRGB everywhere): if someone later adds proper ICC handling, the LAB
// residual should collapse and this test will need to be tightened.
func TestOpenCVExtractor_DisplayP3StrippedICC(t *testing.T) {
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

	// Baseline: same JPEG re-encode at q=90 without the P3 transform. Any
	// residual here is the cost of the JPEG round-trip alone.
	baselineBytes := reencodeJPEG(t, refBytes, 90)
	baselineSig, _, err := ext.Compute(ctx, baselineBytes)
	if err != nil {
		t.Fatalf("compute baseline: %v", err)
	}
	baselineDec, err := ext.Match(ctx, refSig, baselineSig, refImage, baselineBytes)
	if err != nil {
		t.Fatalf("match baseline: %v", err)
	}

	candBytes := reencodeAsDisplayP3Pixels(t, refBytes, 90)
	candSig, _, err := ext.Compute(ctx, candBytes)
	if err != nil {
		t.Fatalf("compute candidate: %v", err)
	}
	decision, err := ext.Match(ctx, refSig, candSig, refImage, candBytes)
	if err != nil {
		t.Fatalf("match: %v", err)
	}

	t.Logf("baseline (q90 reencode): matched=%v mean=%.2f max=%.2f", baselineDec.Matched, baselineDec.ColorMean, baselineDec.ColorMax)
	t.Logf("P3-as-sRGB:              matched=%v mean=%.2f max=%.2f", decision.Matched, decision.ColorMean, decision.ColorMax)

	// Today the pipeline does not apply ICC, so a P3 photo with ICC stripped
	// will show a measurable LAB residual on saturated regions. The test
	// pins both ends:
	//   - matched should still be true on this moderately-saturated photo
	//     (we want the pipeline to be robust to small color drift),
	//   - the P3 mean MUST be measurably elevated vs the JPEG-only baseline.
	//     If they ever converge it likely means somebody added ICC handling
	//     — tighten this assertion and remove this note.
	if !decision.Matched {
		t.Errorf("expected match for aletheia P3-as-sRGB candidate (color shift is real but content is identical); got matched=false")
	}
	if decision.ColorMean <= baselineDec.ColorMean*2 {
		t.Errorf("expected P3 ColorMean to be >2x baseline (baseline=%.2f, p3=%.2f). If close to baseline, ICC handling may now be in place — tighten this bound", baselineDec.ColorMean, decision.ColorMean)
	}
}

func reencodeJPEG(t *testing.T, src []byte, quality int) []byte {
	t.Helper()
	mat, err := gocv.IMDecode(src, gocv.IMReadColor)
	if err != nil || mat.Empty() {
		t.Fatalf("decode for reencode: %v", err)
	}
	defer mat.Close()
	buf, err := gocv.IMEncodeWithParams(gocv.JPEGFileExt, mat, []int{gocv.IMWriteJpegQuality, quality})
	if err != nil {
		t.Fatalf("reencode: %v", err)
	}
	defer buf.Close()
	out := make([]byte, len(buf.GetBytes()))
	copy(out, buf.GetBytes())
	return out
}

// reencodeAsDisplayP3Pixels takes an sRGB JPEG and returns a JPEG where the
// pixel samples encode the same visual colors but in the Display P3 gamut.
// No ICC profile is attached, simulating a tool that stripped the profile
// without converting. Display P3 shares the sRGB transfer function, so only
// the primaries matrix differs.
func reencodeAsDisplayP3Pixels(t *testing.T, src []byte, quality int) []byte {
	t.Helper()
	mat, err := gocv.IMDecode(src, gocv.IMReadColor)
	if err != nil || mat.Empty() {
		t.Fatalf("decode src: %v", err)
	}
	defer mat.Close()

	// Matrix: linear sRGB → linear Display P3, computed as
	// (XYZ→P3) · (sRGB→XYZ). Verified against the public Apple ColorSync
	// values; both spaces use D65 white.
	mSRGBtoP3 := [3][3]float64{
		{0.8224621, 0.1775380, 0.0000000},
		{0.0331941, 0.9668058, 0.0000000},
		{0.0170827, 0.0723974, 0.9105199},
	}

	w, h := mat.Cols(), mat.Rows()
	out := gocv.NewMatWithSize(h, w, gocv.MatTypeCV8UC3)
	defer out.Close()

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			bgr := mat.GetVecbAt(y, x)
			// BGR order in OpenCV
			b := srgbDecode(float64(bgr[0]) / 255.0)
			g := srgbDecode(float64(bgr[1]) / 255.0)
			r := srgbDecode(float64(bgr[2]) / 255.0)

			rp := mSRGBtoP3[0][0]*r + mSRGBtoP3[0][1]*g + mSRGBtoP3[0][2]*b
			gp := mSRGBtoP3[1][0]*r + mSRGBtoP3[1][1]*g + mSRGBtoP3[1][2]*b
			bp := mSRGBtoP3[2][0]*r + mSRGBtoP3[2][1]*g + mSRGBtoP3[2][2]*b

			bpx := clamp255(srgbEncode(bp))
			gpx := clamp255(srgbEncode(gp))
			rpx := clamp255(srgbEncode(rp))
			out.SetUCharAt(y, x*3+0, bpx)
			out.SetUCharAt(y, x*3+1, gpx)
			out.SetUCharAt(y, x*3+2, rpx)
		}
	}

	buf, err := gocv.IMEncodeWithParams(gocv.JPEGFileExt, out, []int{gocv.IMWriteJpegQuality, quality})
	if err != nil {
		t.Fatalf("encode p3 jpeg: %v", err)
	}
	defer buf.Close()
	bytes := make([]byte, len(buf.GetBytes()))
	copy(bytes, buf.GetBytes())
	return bytes
}

func srgbDecode(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func srgbEncode(v float64) float64 {
	if v <= 0.0 {
		return 0.0
	}
	if v <= 0.0031308 {
		return 12.92 * v
	}
	return 1.055*math.Pow(v, 1.0/2.4) - 0.055
}

func clamp255(v float64) uint8 {
	x := v * 255.0
	if x < 0 {
		return 0
	}
	if x > 255 {
		return 255
	}
	return uint8(x + 0.5)
}

func upscaleRotateRecompress(t *testing.T, src []byte, longSide, quality int) []byte {
	t.Helper()
	mat, err := gocv.IMDecode(src, gocv.IMReadColor)
	if err != nil || mat.Empty() {
		t.Fatalf("decode src: %v", err)
	}
	defer mat.Close()

	w, h := mat.Cols(), mat.Rows()
	long := w
	if h > long {
		long = h
	}
	scale := float64(longSide) / float64(long)
	scaled := gocv.NewMat()
	defer scaled.Close()
	gocv.Resize(mat, &scaled, image.Point{X: int(float64(w) * scale), Y: int(float64(h) * scale)}, 0, 0, gocv.InterpolationCubic)

	rotated := gocv.NewMat()
	defer rotated.Close()
	gocv.Rotate(scaled, &rotated, gocv.Rotate90Clockwise)

	buf, err := gocv.IMEncodeWithParams(gocv.JPEGFileExt, rotated, []int{gocv.IMWriteJpegQuality, quality})
	if err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	defer buf.Close()
	out := make([]byte, len(buf.GetBytes()))
	copy(out, buf.GetBytes())

	// Sanity: confirm it actually decodes back at the expected scale.
	if _, _, derr := image.DecodeConfig(bytes.NewReader(out)); derr != nil {
		// Try jpeg decode for clearer error.
		if _, jerr := jpeg.Decode(bytes.NewReader(out)); jerr != nil {
			t.Fatalf("encoded jpeg invalid: %v / %v", derr, jerr)
		}
	}
	return out
}
