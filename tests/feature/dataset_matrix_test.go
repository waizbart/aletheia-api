//go:build integration

package feature_test

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gocv.io/x/gocv"

	"github.com/waizbart/aletheia-api/internal/domain"
	"github.com/waizbart/aletheia-api/internal/feature"
)

const datasetBaseDir = "testdata/dataset/base"

// variant describes one transformation applied to a base image.
type variant struct {
	name   string
	expect bool // true = should match the base; false = should not
	build  func(t *testing.T, baseBytes []byte, peerBytes []byte) []byte
}

// TestDataset_FullMatrix exercises the matcher against a battery of common
// real-world transformations across 20 unrelated base photos. It generates
// each variant in memory, runs Match against the base, and reports a
// confusion matrix + per-variant pass rate. Use to sanity-check threshold
// changes against a richer dataset than the curated aletheia matrix.
func TestDataset_FullMatrix(t *testing.T) {
	ctx := context.Background()
	ext := feature.NewOpenCVExtractor()
	defer ext.Close()

	files, err := listBaseImages(t)
	if err != nil {
		t.Fatalf("list base: %v", err)
	}
	if len(files) < 20 {
		t.Fatalf("need >=20 base images, got %d", len(files))
	}

	// Load every base once and cache its signature + stored blob.
	type baseEntry struct {
		name     string
		bytes    []byte
		sig      *domain.FeatureSignature
		refImage []byte
	}
	bases := make([]baseEntry, 0, len(files))
	for _, fp := range files {
		b, err := os.ReadFile(fp)
		if err != nil {
			t.Fatalf("read %s: %v", fp, err)
		}
		sig, refImage, err := ext.Compute(ctx, b)
		if err != nil {
			t.Logf("skip %s: extractor rejected base (%v)", filepath.Base(fp), err)
			continue
		}
		bases = append(bases, baseEntry{
			name:     filepath.Base(fp),
			bytes:    b,
			sig:      sig,
			refImage: refImage,
		})
	}
	if len(bases) < 20 {
		t.Fatalf("only %d/%d bases produced features", len(bases), len(files))
	}

	variants := buildVariants()

	type cell struct {
		variant string
		expect  bool
		pass    int
		fail    int
		// keep first failure metrics for debugging
		firstFailBase string
		firstFailDec  string
	}
	cells := make(map[string]*cell, len(variants))
	for _, v := range variants {
		cells[v.name] = &cell{variant: v.name, expect: v.expect}
	}

	var tp, fp, tn, fn int

	for i, base := range bases {
		// Peer for "different image" negatives: pair with next base (cyclic).
		peer := bases[(i+1)%len(bases)]
		for _, v := range variants {
			candBytes := v.build(t, base.bytes, peer.bytes)
			candSigRaw, _, cerr := ext.Compute(ctx, candBytes)
			c := cells[v.name]

			matched := false
			var detail string
			if cerr != nil {
				detail = fmt.Sprintf("extract err: %v", cerr)
			} else {
				dec, merr := ext.Match(ctx, base.sig, candSigRaw, base.refImage, candBytes)
				if merr != nil {
					detail = fmt.Sprintf("match err: %v", merr)
				} else {
					matched = dec.Matched
					detail = fmt.Sprintf("inliers=%d mean=%.2f max=%.2f cells=%d",
						dec.Inliers, dec.ColorMean, dec.ColorMax, dec.Cells)
				}
			}

			correct := matched == v.expect
			if correct {
				c.pass++
			} else {
				c.fail++
				if c.firstFailBase == "" {
					c.firstFailBase = base.name
					c.firstFailDec = detail
				}
			}
			if v.expect {
				if matched {
					tp++
				} else {
					fn++
				}
			} else {
				if matched {
					fp++
				} else {
					tn++
				}
			}
		}
	}

	// Sort by variant name for stable report.
	names := make([]string, 0, len(cells))
	for n := range cells {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	fmt.Fprintf(&b, "\n=== DATASET MATRIX REPORT ===\n")
	fmt.Fprintf(&b, "Bases: %d   Variants/base: %d   Total pairs: %d\n\n",
		len(bases), len(variants), len(bases)*len(variants))
	fmt.Fprintf(&b, "%-28s %-9s %-7s %s\n", "variant", "expects", "pass", "first-fail")
	fmt.Fprintln(&b, strings.Repeat("-", 90))
	for _, n := range names {
		c := cells[n]
		exp := "match"
		if !c.expect {
			exp = "REJECT"
		}
		ratio := fmt.Sprintf("%d/%d", c.pass, c.pass+c.fail)
		first := ""
		if c.fail > 0 {
			first = c.firstFailBase + " " + c.firstFailDec
		}
		fmt.Fprintf(&b, "%-28s %-9s %-7s %s\n", c.variant, exp, ratio, first)
	}
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "Confusion matrix\n")
	fmt.Fprintf(&b, "  TP=%4d  FP=%4d\n  FN=%4d  TN=%4d\n", tp, fp, fn, tn)
	precision := safeDiv(tp, tp+fp)
	recall := safeDiv(tp, tp+fn)
	f1 := 0.0
	if precision+recall > 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}
	fmt.Fprintf(&b, "Precision=%.3f  Recall=%.3f  F1=%.3f\n", precision, recall, f1)

	t.Log(b.String())

	// Pass gates calibrated against the current matcher.
	//
	// Precision is strict (>=0.95): false positives are the worst failure mode
	// for certification — you'd certify a different image as the original.
	//
	// Recall is intentionally looser (>=0.75) because this dataset includes
	// rotation + JPEG re-encode variants on contrast-heavy real photos, which
	// produce ringing along sky/building edges that pushes the per-cell max
	// past MaxCellDist=38. The trade-off is documented: tightening recall
	// here would require either a structurally different residual metric
	// (e.g. spatial clustering of hot cells) or relaxing the per-cell max,
	// which empirically breaks the curated changed-2 / changed-* rejects.
	if precision < 0.95 {
		t.Errorf("precision %.3f below 0.95 — false-positive rate too high", precision)
	}
	if recall < 0.75 {
		t.Errorf("recall %.3f below 0.75 — false-negative rate too high", recall)
	}
}

func listBaseImages(t *testing.T) ([]string, error) {
	t.Helper()
	entries, err := os.ReadDir(datasetBaseDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".jpeg") || strings.HasSuffix(name, ".png") {
			out = append(out, filepath.Join(datasetBaseDir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

func safeDiv(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// ---------------------------------------------------------------------------
// Variant builders
// ---------------------------------------------------------------------------

func buildVariants() []variant {
	return []variant{
		// --- Positives: should match ---
		{name: "jpeg_q80", expect: true, build: jpegRecompress(80)},
		{name: "jpeg_q50", expect: true, build: jpegRecompress(50)},
		{name: "jpeg_q30", expect: true, build: jpegRecompress(30)},
		{name: "jpeg_q10", expect: true, build: jpegRecompress(10)},
		{name: "resize_50pct_q70", expect: true, build: resizeThenRecompress(0.5, 70)},
		{name: "whatsapp_like_q40_960px", expect: true, build: whatsappLike(960, 40)},
		{name: "rotate_90", expect: true, build: rotateVariant(gocv.Rotate90Clockwise)},
		{name: "rotate_180", expect: true, build: rotateVariant(gocv.Rotate180Clockwise)},
		{name: "rotate_270", expect: true, build: rotateVariant(gocv.Rotate90CounterClockwise)},
		{name: "crop_5pct_margin", expect: true, build: cropMargin(0.05)},
		{name: "crop_10pct_margin", expect: true, build: cropMargin(0.10)},
		{name: "p3_as_srgb_q70", expect: true, build: p3Variant(70)},

		// --- Negatives: should NOT match ---
		{name: "heavy_saturation_filter", expect: false, build: saturationFilter(2.0, 1.2)},
		{name: "color_invert", expect: false, build: invertColors()},
		{name: "rectangle_overlay_15pct", expect: false, build: overlayRectangle(0.15)},
		{name: "different_image", expect: false, build: returnPeer()},
	}
}

func decodeBGRMat(t *testing.T, b []byte) gocv.Mat {
	t.Helper()
	m, err := gocv.IMDecode(b, gocv.IMReadColor)
	if err != nil || m.Empty() {
		t.Fatalf("decode: %v", err)
	}
	return m
}

func encodeJPEG(t *testing.T, m gocv.Mat, quality int) []byte {
	t.Helper()
	buf, err := gocv.IMEncodeWithParams(gocv.JPEGFileExt, m, []int{gocv.IMWriteJpegQuality, quality})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	defer buf.Close()
	out := make([]byte, len(buf.GetBytes()))
	copy(out, buf.GetBytes())
	return out
}

func jpegRecompress(q int) func(t *testing.T, base, peer []byte) []byte {
	return func(t *testing.T, base, _ []byte) []byte {
		m := decodeBGRMat(t, base)
		defer m.Close()
		return encodeJPEG(t, m, q)
	}
}

func resizeThenRecompress(scale float64, q int) func(t *testing.T, base, peer []byte) []byte {
	return func(t *testing.T, base, _ []byte) []byte {
		m := decodeBGRMat(t, base)
		defer m.Close()
		dst := gocv.NewMat()
		defer dst.Close()
		gocv.Resize(m, &dst, image.Point{X: int(float64(m.Cols()) * scale), Y: int(float64(m.Rows()) * scale)}, 0, 0, gocv.InterpolationArea)
		return encodeJPEG(t, dst, q)
	}
}

// whatsappLike: cap long side at maxLong and re-encode at a low quality with
// chroma subsampling 4:2:0 (which IMWriteJpegQuality enables by default below
// 90). This approximates the heavy recompression WhatsApp applies.
func whatsappLike(maxLong, q int) func(t *testing.T, base, peer []byte) []byte {
	return func(t *testing.T, base, _ []byte) []byte {
		m := decodeBGRMat(t, base)
		defer m.Close()
		long := m.Cols()
		if m.Rows() > long {
			long = m.Rows()
		}
		if long <= maxLong {
			return encodeJPEG(t, m, q)
		}
		scale := float64(maxLong) / float64(long)
		dst := gocv.NewMat()
		defer dst.Close()
		gocv.Resize(m, &dst, image.Point{X: int(float64(m.Cols()) * scale), Y: int(float64(m.Rows()) * scale)}, 0, 0, gocv.InterpolationArea)
		return encodeJPEG(t, dst, q)
	}
}

func rotateVariant(code gocv.RotateFlag) func(t *testing.T, base, peer []byte) []byte {
	return func(t *testing.T, base, _ []byte) []byte {
		m := decodeBGRMat(t, base)
		defer m.Close()
		dst := gocv.NewMat()
		defer dst.Close()
		gocv.Rotate(m, &dst, code)
		return encodeJPEG(t, dst, 90)
	}
}

func cropMargin(frac float64) func(t *testing.T, base, peer []byte) []byte {
	return func(t *testing.T, base, _ []byte) []byte {
		m := decodeBGRMat(t, base)
		defer m.Close()
		w, h := m.Cols(), m.Rows()
		dx := int(float64(w) * frac)
		dy := int(float64(h) * frac)
		rect := image.Rect(dx, dy, w-dx, h-dy)
		region := m.Region(rect)
		defer region.Close()
		clone := region.Clone()
		defer clone.Close()
		return encodeJPEG(t, clone, 85)
	}
}

func p3Variant(q int) func(t *testing.T, base, peer []byte) []byte {
	return func(t *testing.T, base, _ []byte) []byte {
		return reencodeAsDisplayP3Pixels(t, base, q)
	}
}

// saturationFilter shifts hue saturation by satMul and value by valMul (in
// HSV space). Strong filter that mimics an Instagram-style heavy edit.
func saturationFilter(satMul, valMul float64) func(t *testing.T, base, peer []byte) []byte {
	return func(t *testing.T, base, _ []byte) []byte {
		m := decodeBGRMat(t, base)
		defer m.Close()
		hsv := gocv.NewMat()
		defer hsv.Close()
		gocv.CvtColor(m, &hsv, gocv.ColorBGRToHSV)

		channels := gocv.Split(hsv)
		// channels[0]=H, [1]=S, [2]=V
		s := gocv.NewMat()
		defer s.Close()
		channels[1].ConvertToWithParams(&s, gocv.MatTypeCV8U, float32(satMul), 0)
		v := gocv.NewMat()
		defer v.Close()
		channels[2].ConvertToWithParams(&v, gocv.MatTypeCV8U, float32(valMul), 0)
		// rebuild
		hsvOut := gocv.NewMat()
		defer hsvOut.Close()
		gocv.Merge([]gocv.Mat{channels[0], s, v}, &hsvOut)
		for _, c := range channels {
			c.Close()
		}
		out := gocv.NewMat()
		defer out.Close()
		gocv.CvtColor(hsvOut, &out, gocv.ColorHSVToBGR)
		return encodeJPEG(t, out, 85)
	}
}

// invertColors: photographic negative — content is intact but pixel values
// are reversed. ORB descriptors are intensity-based, so most matches break;
// the few that survive should not pass the color residual either.
func invertColors() func(t *testing.T, base, peer []byte) []byte {
	return func(t *testing.T, base, _ []byte) []byte {
		m := decodeBGRMat(t, base)
		defer m.Close()
		inv := gocv.NewMat()
		defer inv.Close()
		gocv.BitwiseNot(m, &inv)
		return encodeJPEG(t, inv, 85)
	}
}

// overlayRectangle paints a solid 15% rectangle on top of the image. Even at
// 15% coverage the homography usually still fits, so the matcher must reject
// via the per-cell color max.
func overlayRectangle(coverageFrac float64) func(t *testing.T, base, peer []byte) []byte {
	return func(t *testing.T, base, _ []byte) []byte {
		m := decodeBGRMat(t, base)
		defer m.Close()
		w, h := m.Cols(), m.Rows()
		// Square overlay sized so area ~= coverageFrac of image.
		// area = side² → side = sqrt(coverageFrac * w * h)
		side := int(float64(w*h) * coverageFrac)
		s := 1
		for s*s < side {
			s++
		}
		if s > w/2 {
			s = w / 2
		}
		if s > h/2 {
			s = h / 2
		}
		// Place near top-left, offset by 5% to avoid touching the edge.
		x0 := int(float64(w) * 0.05)
		y0 := int(float64(h) * 0.05)
		clone := m.Clone()
		defer clone.Close()
		gocv.Rectangle(&clone, image.Rect(x0, y0, x0+s, y0+s), color.RGBA{255, 0, 255, 0}, -1)
		return encodeJPEG(t, clone, 85)
	}
}

// returnPeer simply returns the peer image bytes. Used to assert that
// unrelated images are not matched.
func returnPeer() func(t *testing.T, base, peer []byte) []byte {
	return func(_ *testing.T, _, peer []byte) []byte {
		return peer
	}
}
